// Package tool defines the typed, provider-neutral tool contract used by the
// agent runtime.
//
// A Tool describes itself with a Spec and executes a Call. Constructors in
// this package generate JSON Schema from Go input types and validate model
// arguments before a handler observes them.
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ExecutionMode controls how a tool may be scheduled.
type ExecutionMode string

const (
	// ExecutionParallel allows the runner to execute the tool alongside other
	// calls from the same assistant message.
	ExecutionParallel ExecutionMode = "parallel"
	// ExecutionSequential requires the runner to finish this call before
	// starting the next one.
	ExecutionSequential ExecutionMode = "sequential"
)

// Annotations describes a tool's side effects.
type Annotations struct {
	// ReadOnly means the tool does not mutate state.
	ReadOnly bool
	// Destructive means the tool may delete or overwrite data.
	Destructive bool
	// Idempotent means repeating the same call has the same effect.
	Idempotent bool
	// OpenWorld means the tool interacts with an unbounded external system.
	OpenWorld bool
}

// Spec is the immutable description of a tool.
type Spec struct {
	// Name is the unique tool name exposed to the model.
	Name string
	// Description explains what the tool does and when to use it.
	Description string
	// InputSchema is the JSON Schema for the tool input.
	InputSchema map[string]any
	// Annotations describes side effects.
	Annotations Annotations
	// ExecutionMode controls scheduling.
	ExecutionMode ExecutionMode
	// Strict asks the provider to enforce the input schema exactly.
	Strict bool
}

// Call is one model-requested tool invocation.
type Call struct {
	// ID is the unique call identifier. The runner guarantees it is non-empty
	// and unique within a run.
	ID string
	// Index is the call position within the assistant message.
	Index int
	// Name is the requested tool name.
	Name string
	// Arguments is the validated JSON input.
	Arguments json.RawMessage
	// IdempotencyKey is stable across retries of the same call when the runner
	// can derive one.
	IdempotencyKey string
}

// Tool is an executable capability.
type Tool interface {
	// Spec returns the tool description.
	Spec() Spec
	// Execute runs the tool. Implementations must honor ctx.
	Execute(ctx context.Context, call Call) (Result, error)
}

var (
	// ErrIDRequired is returned when creating a tool without a name.
	ErrIDRequired = fmt.Errorf("tool: id is required")
	// ErrExecuteRequired is returned when creating a tool without a handler.
	ErrExecuteRequired = fmt.Errorf("tool: execute function is required")
	// ErrInvalidSchema is returned when a tool's parameter schema cannot be
	// built or is not a JSON object.
	ErrInvalidSchema = fmt.Errorf("tool: invalid schema")
	// ErrInvalidInput is returned when tool input cannot be decoded or does
	// not satisfy the tool schema.
	ErrInvalidInput = fmt.Errorf("tool: invalid input")
	// ErrOutputEncode is returned when a tool output cannot be encoded.
	ErrOutputEncode = fmt.Errorf("tool: cannot encode output")
)

// Validator is an optional interface for input types that validate themselves
// after JSON decoding and schema validation.
type Validator interface {
	Validate() error
}

// ExecuteFunc is the typed execution function of a tool.
type ExecuteFunc[In any, Out any] func(ctx context.Context, input In) (Out, error)

// Config configures a typed tool.
type Config[In any, Out any] struct {
	// ID is the unique tool name. Required.
	ID string
	// Description explains the tool.
	Description string
	// Parameters overrides the generated JSON Schema. If nil, a schema is
	// generated from In.
	Parameters map[string]any
	// SchemaOptions configures generated schemas.
	SchemaOptions []SchemaOption
	// Annotations describes side effects.
	Annotations Annotations
	// ExecutionMode controls scheduling. The zero value is parallel.
	ExecutionMode ExecutionMode
	// Strict marks the schema as strict for providers that support it.
	Strict bool
	// Execute is the typed handler. Required.
	Execute ExecuteFunc[In, Out]
}

// BaseTool is the generic, typed implementation of Tool.
type BaseTool[In any, Out any] struct {
	spec    Spec
	execute ExecuteFunc[In, Out]
}

// New creates a typed tool. It fails on missing configuration, unsupported
// input types, or an invalid custom schema.
func New[In any, Out any](cfg Config[In, Out]) (*BaseTool[In, Out], error) {
	name := strings.TrimSpace(cfg.ID)
	if name == "" {
		return nil, ErrIDRequired
	}
	if cfg.Execute == nil {
		return nil, ErrExecuteRequired
	}

	var schema map[string]any
	if cfg.Parameters != nil {
		normalized, err := normalizeSchema(cfg.Parameters)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
		}
		schema = normalized
	} else {
		generated, err := GenerateSchema[In](cfg.SchemaOptions...)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
		}
		schema = generated
	}

	mode := cfg.ExecutionMode
	if mode == "" {
		mode = ExecutionParallel
	}

	return &BaseTool[In, Out]{
		spec: Spec{
			Name:          name,
			Description:   cfg.Description,
			InputSchema:   schema,
			Annotations:   cfg.Annotations,
			ExecutionMode: mode,
			Strict:        cfg.Strict,
		},
		execute: cfg.Execute,
	}, nil
}

// MustNew creates a tool or panics on invalid configuration.
func MustNew[In any, Out any](cfg Config[In, Out]) *BaseTool[In, Out] {
	t, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return t
}

// Spec returns a copy of the tool specification.
func (t *BaseTool[In, Out]) Spec() Spec {
	if t == nil {
		return Spec{}
	}
	return Spec{
		Name:          t.spec.Name,
		Description:   t.spec.Description,
		InputSchema:   cloneSchemaMap(t.spec.InputSchema),
		Annotations:   t.spec.Annotations,
		ExecutionMode: t.spec.ExecutionMode,
		Strict:        t.spec.Strict,
	}
}

// ID returns the tool name.
func (t *BaseTool[In, Out]) ID() string {
	if t == nil {
		return ""
	}
	return t.spec.Name
}

// Description returns the tool description.
func (t *BaseTool[In, Out]) Description() string {
	if t == nil {
		return ""
	}
	return t.spec.Description
}

// Parameters returns a copy of the input schema.
func (t *BaseTool[In, Out]) Parameters() map[string]any {
	if t == nil {
		return nil
	}
	return cloneSchemaMap(t.spec.InputSchema)
}

// Execute validates and decodes the call arguments, invokes the handler, and
// normalizes the output.
func (t *BaseTool[In, Out]) Execute(ctx context.Context, call Call) (Result, error) {
	if t == nil || t.execute == nil {
		return Result{}, ErrExecuteRequired
	}
	if t.spec.Name == "" {
		return Result{}, ErrIDRequired
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("tool %s: %w", t.spec.Name, err)
	}

	// Empty or null arguments mean "no arguments": the handler observes the
	// zero value. Explicit JSON (including "{}") is always validated.
	input := bytes.TrimSpace(call.Arguments)
	var in In
	if len(input) > 0 && !bytes.Equal(input, []byte("null")) {
		if err := Validate(t.spec.InputSchema, input); err != nil {
			return Result{}, fmt.Errorf("%w: tool %s: %w", ErrInvalidInput, t.spec.Name, err)
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{}, fmt.Errorf("%w: tool %s: %w", ErrInvalidInput, t.spec.Name, err)
		}
	}
	if validator, ok := any(in).(Validator); ok {
		if err := validator.Validate(); err != nil {
			return Result{}, fmt.Errorf("%w: tool %s: %w", ErrInvalidInput, t.spec.Name, err)
		}
	}

	out, err := t.execute(ctx, in)
	if err != nil {
		return Result{}, err
	}
	result, err := resultFromOutput(out)
	if err != nil {
		return Result{}, fmt.Errorf("tool %s: %w", t.spec.Name, err)
	}
	return result, nil
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

func normalizeSchema(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, fmt.Errorf("schema must be a JSON object")
	}
	if _, err := json.Marshal(value); err != nil {
		return nil, err
	}
	return cloneSchemaMap(value), nil
}

func cloneSchemaMap(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch value := v.(type) {
		case map[string]any:
			out[k] = cloneSchemaMap(value)
		case []any:
			items := make([]any, len(value))
			for i, item := range value {
				if nested, ok := item.(map[string]any); ok {
					items[i] = cloneSchemaMap(nested)
				} else {
					items[i] = item
				}
			}
			out[k] = items
		case []string:
			out[k] = append([]string(nil), value...)
		default:
			out[k] = v
		}
	}
	return out
}
