package llm

import "encoding/json"

// ToolCall represents a model-requested invocation of an external tool.
//
// Index identifies the position of the call within a single assistant
// message. Providers that stream partial tool calls use it to associate
// argument fragments with the right call. Providers that do not expose an
// index may leave it zero; the runner assigns stable indices when finalizing
// a message.
type ToolCall struct {
	// Index is the zero-based position of the call in the assistant message.
	Index int `json:"index,omitempty"`
	// ID is the provider-assigned call identifier. It may be empty from some
	// providers; the runner assigns a unique identifier before execution.
	ID string `json:"id"`
	// Name is the tool name requested by the model.
	Name string `json:"name"`
	// Arguments is the JSON-encoded arguments object. During streaming it may
	// contain a fragment; the runner concatenates fragments by Index.
	Arguments string `json:"arguments"`
}

// UnmarshalArgs unmarshals the call's JSON arguments into target.
func (tc ToolCall) UnmarshalArgs(target any) error {
	if tc.Arguments == "" {
		return nil
	}
	return json.Unmarshal([]byte(tc.Arguments), target)
}

// ToolDefinition describes a tool schema exposed to the model.
type ToolDefinition struct {
	// Name is the unique tool name.
	Name string `json:"name"`
	// Description explains what the tool does and when to use it.
	Description string `json:"description"`
	// Parameters is the JSON Schema for the tool input.
	Parameters map[string]any `json:"parameters,omitempty"`
	// Strict asks the provider to enforce the schema when supported.
	Strict bool `json:"strict,omitempty"`
}

// ToolChoiceMode controls whether and how the model may call tools.
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide.
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceNone forbids tool calls.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceRequired requires at least one tool call.
	ToolChoiceRequired ToolChoiceMode = "required"
)

// ToolChoice constrains tool selection. An empty Mode means provider default.
type ToolChoice struct {
	// Mode selects whether and how the model may call tools.
	Mode ToolChoiceMode `json:"mode,omitempty"`
	// Name optionally forces a specific tool. Providers that do not support a
	// named choice may ignore it.
	Name string `json:"name,omitempty"`
}
