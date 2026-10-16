// Package tool provides interfaces and constructors for creating modular,
// LLM-callable tools with automatic schema generation and typed execution.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrIDRequired is returned when creating a Tool without an ID.
	ErrIDRequired = errors.New("tool: id is required")
	// ErrExecuteRequired is returned when creating a Tool without an execution function.
	ErrExecuteRequired = errors.New("tool: execute function is required")
)

// Tool represents an executable action that an Agent or LLM can invoke.
type Tool interface {
	// ID returns the unique name or identifier of the tool (e.g. "calculator", "get_weather").
	ID() string
	// Description returns a description of what the tool does, used by the LLM for tool selection.
	Description() string
	// Parameters returns the parameter schema for the tool (e.g. JSON schema map).
	Parameters() any
	// Execute runs the tool with the provided raw input JSON arguments.
	Execute(ctx context.Context, input []byte) (any, error)
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
	// Execute is the strongly typed handler function. Required.
	Execute ExecuteFunc[In, Out]
}

// BaseTool is a generic, typed implementation of the Tool interface.
type BaseTool[In any, Out any] struct {
	id          string
	description string
	parameters  any
	execute     ExecuteFunc[In, Out]
}

// New creates and validates a new generic BaseTool.
// If cfg.Parameters is nil, a JSON Schema is automatically generated from the In type.
func New[In any, Out any](cfg Config[In, Out]) (*BaseTool[In, Out], error) {
	if cfg.ID == "" {
		return nil, ErrIDRequired
	}
	if cfg.Execute == nil {
		return nil, ErrExecuteRequired
	}

	params := cfg.Parameters
	if params == nil {
		params = GenerateSchema[In]()
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
	return t.id
}

// Description returns the description of the tool.
func (t *BaseTool[In, Out]) Description() string {
	return t.description
}

// Parameters returns the tool's parameter schema.
func (t *BaseTool[In, Out]) Parameters() any {
	return t.parameters
}

// Execute executes the tool by unmarshaling raw JSON input into In and invoking the handler.
func (t *BaseTool[In, Out]) Execute(ctx context.Context, input []byte) (any, error) {
	var in In

	switch target := any(&in).(type) {
	case *[]byte:
		*target = input
	case *string:
		*target = string(input)
	default:
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, fmt.Errorf("tool %s: invalid input: %w", t.id, err)
			}
		}
	}

	return t.execute(ctx, in)
}

// ExecuteTyped runs the tool with strongly-typed input directly.
func (t *BaseTool[In, Out]) ExecuteTyped(ctx context.Context, input In) (Out, error) {
	return t.execute(ctx, input)
}

// Definition returns the tool definition suitable for LLM requests.
func (t *BaseTool[In, Out]) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        t.id,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

// ToDefinition converts any Tool to an llm.ToolDefinition.
func ToDefinition(t Tool) llm.ToolDefinition {
	if definer, ok := t.(interface{ Definition() llm.ToolDefinition }); ok {
		return definer.Definition()
	}
	return llm.ToolDefinition{
		Name:        t.ID(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}

// ToDefinitions converts a slice of Tools to a slice of llm.ToolDefinition.
func ToDefinitions(tools []Tool) []llm.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	defs := make([]llm.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = ToDefinition(t)
	}
	return defs
}
