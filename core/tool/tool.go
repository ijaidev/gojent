// Package tool provides interfaces and constructors for creating modular,
// LLM-callable tools with automatic schema generation and typed execution.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var (
	// ErrIDRequired is returned when creating a Tool without an ID.
	ErrIDRequired = errors.New("tool: id is required")
	// ErrExecuteRequired is returned when creating a Tool without an execution function.
	ErrExecuteRequired = errors.New("tool: execute function is required")
	// ErrInvalidSchema is returned when a tool's parameter schema cannot be built
	// or is not a valid JSON object.
	ErrInvalidSchema = errors.New("tool: invalid schema")
	// ErrInvalidInput is returned when a tool's input cannot be decoded or validated.
	ErrInvalidInput = errors.New("tool: invalid input")
	// ErrOutputEncode is returned when a tool's output cannot be encoded for the model.
	ErrOutputEncode = errors.New("tool: cannot encode output")
)

// Validator is an optional interface for tool input types that need validation
// after JSON decoding and before execution.
type Validator interface {
	Validate() error
}

// Tool represents an executable action that an Agent or LLM can invoke.
type Tool interface {
	// ID returns the unique name or identifier of the tool (e.g. "calculator", "get_weather").
	ID() string
	// Description returns a description of what the tool does, used by the LLM for tool selection.
	Description() string
	// Parameters returns the parameter JSON Schema object for the tool.
	Parameters() map[string]any
	// Execute runs the tool with the provided raw input JSON arguments.
	Execute(ctx context.Context, input []byte) (Result, error)
}

// ExecuteFunc defines the typed execution logic of a tool.
type ExecuteFunc[In any, Out any] func(ctx context.Context, input In) (Out, error)

// Config specifies the configuration options for creating a new Tool.
type Config[In any, Out any] struct {
	// ID is the unique name/identifier for the tool (e.g. "get_weather"). Required.
	ID string
	// Description describes the purpose and capability of the tool.
	Description string
	// Parameters is an optional custom schema override. If nil, schema is generated from In.
	Parameters any
	// SchemaOptions configures automatic schema generation when Parameters is nil.
	SchemaOptions []SchemaOption
	// Execute is the strongly typed handler function. Required.
	Execute ExecuteFunc[In, Out]
}

// BaseTool is a generic, typed implementation of the Tool interface.
type BaseTool[In any, Out any] struct {
	id          string
	description string
	parameters  map[string]any
	execute     ExecuteFunc[In, Out]
}

// New creates and validates a new generic BaseTool.
// If cfg.Parameters is nil, a JSON Schema is automatically generated from the In type.
func New[In any, Out any](cfg Config[In, Out]) (*BaseTool[In, Out], error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, ErrIDRequired
	}
	if cfg.Execute == nil {
		return nil, ErrExecuteRequired
	}

	var params map[string]any
	if cfg.Parameters == nil {
		generated, err := GenerateSchemaE[In](cfg.SchemaOptions...)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
		}
		params = generated
	} else {
		normalized, err := normalizeSchema(cfg.Parameters)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
		}
		params = normalized
	}

	return &BaseTool[In, Out]{
		id:          cfg.ID,
		description: cfg.Description,
		parameters:  params,
		execute:     cfg.Execute,
	}, nil
}

// MustNew creates a new generic BaseTool or panics if configuration validation fails.
func MustNew[In any, Out any](cfg Config[In, Out]) *BaseTool[In, Out] {
	t, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return t
}

// ID returns the unique identifier of the tool.
func (t *BaseTool[In, Out]) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// Description returns the description of the tool.
func (t *BaseTool[In, Out]) Description() string {
	if t == nil {
		return ""
	}
	return t.description
}

// Parameters returns a copy of the tool's parameter schema.
func (t *BaseTool[In, Out]) Parameters() map[string]any {
	if t == nil || t.parameters == nil {
		return nil
	}
	params, err := cloneJSONValue(t.parameters)
	if err != nil {
		return nil
	}
	schema, _ := params.(map[string]any)
	return schema
}

// Execute decodes raw JSON input into In, validates it when supported, invokes
// the handler, and normalizes the handler output into a Result.
func (t *BaseTool[In, Out]) Execute(ctx context.Context, input []byte) (Result, error) {
	if t == nil || t.execute == nil {
		return Result{}, ErrExecuteRequired
	}
	if t.id == "" {
		return Result{}, ErrIDRequired
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("tool %s: %w", t.id, err)
	}

	var in In
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{}, fmt.Errorf("%w: tool %s: %w", ErrInvalidInput, t.id, err)
		}
	}
	if validator, ok := any(in).(Validator); ok {
		if err := validator.Validate(); err != nil {
			return Result{}, fmt.Errorf("%w: tool %s: %w", ErrInvalidInput, t.id, err)
		}
	}

	out, err := t.execute(ctx, in)
	if err != nil {
		return Result{}, err
	}
	result, err := resultFromOutput(out)
	if err != nil {
		return Result{}, fmt.Errorf("tool %s: %w", t.id, err)
	}
	return result, nil
}

// ExecuteTyped runs the tool with strongly-typed input directly.
func (t *BaseTool[In, Out]) ExecuteTyped(ctx context.Context, input In) (Out, error) {
	var zero Out
	if t == nil || t.execute == nil {
		return zero, ErrExecuteRequired
	}
	return t.execute(ctx, input)
}

// IsNilTool reports whether t is nil or wraps a typed nil implementation.
func IsNilTool(t Tool) bool {
	if t == nil {
		return true
	}
	v := reflect.ValueOf(t)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// normalizeSchema validates a custom schema and returns an isolated copy.
// A schema supplied as map[string]any keeps its concrete value types; other
// JSON object representations are normalized through encoding/json.
func normalizeSchema(value any) (map[string]any, error) {
	if schema, ok := value.(map[string]any); ok {
		if schema == nil {
			return nil, errors.New("schema must be a JSON object")
		}
		if _, err := json.Marshal(schema); err != nil {
			return nil, err
		}
		cloned, err := cloneJSONValue(schema)
		if err != nil {
			return nil, err
		}
		normalized, ok := cloned.(map[string]any)
		if !ok || normalized == nil {
			return nil, errors.New("schema must be a JSON object")
		}
		return normalized, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err := json.Unmarshal(data, &normalized); err != nil || normalized == nil {
		return nil, errors.New("schema must be a JSON object")
	}
	return normalized, nil
}

// cloneJSONValue deep-copies a JSON-compatible value while preserving concrete
// map and slice types. It is used to keep caller-owned schemas isolated.
func cloneJSONValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	return cloneValue(reflect.ValueOf(value)), nil
}

func cloneValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return cloneValue(value.Elem())
	case reflect.Pointer:
		if value.IsNil() {
			return value.Interface()
		}
		clonedValue := cloneValue(value.Elem())
		if clonedValue == nil {
			return reflect.Zero(value.Type()).Interface()
		}
		reflected := reflect.ValueOf(clonedValue)
		if !reflected.Type().AssignableTo(value.Type().Elem()) {
			return value.Interface()
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(reflected)
		return cloned.Interface()
	case reflect.Map:
		if value.IsNil() {
			return value.Interface()
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			clonedValue := cloneValue(iter.Value())
			if clonedValue == nil {
				cloned.SetMapIndex(iter.Key(), reflect.Zero(iter.Value().Type()))
				continue
			}
			reflected := reflect.ValueOf(clonedValue)
			if !reflected.Type().AssignableTo(iter.Value().Type()) {
				reflected = reflected.Convert(iter.Value().Type())
			}
			cloned.SetMapIndex(iter.Key(), reflected)
		}
		return cloned.Interface()
	case reflect.Slice:
		if value.IsNil() {
			return value.Interface()
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			clonedValue := cloneValue(value.Index(i))
			if clonedValue == nil {
				continue
			}
			reflected := reflect.ValueOf(clonedValue)
			if !reflected.Type().AssignableTo(value.Type().Elem()) {
				reflected = reflected.Convert(value.Type().Elem())
			}
			cloned.Index(i).Set(reflected)
		}
		return cloned.Interface()
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			clonedValue := cloneValue(value.Index(i))
			if clonedValue == nil {
				continue
			}
			reflected := reflect.ValueOf(clonedValue)
			if !reflected.Type().AssignableTo(value.Type().Elem()) {
				reflected = reflected.Convert(value.Type().Elem())
			}
			cloned.Index(i).Set(reflected)
		}
		return cloned.Interface()
	default:
		return value.Interface()
	}
}
