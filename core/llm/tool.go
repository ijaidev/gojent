package llm

import "encoding/json"

// ToolCall represents a model-requested invocation of an external tool or function.
type ToolCall struct {
	// ID is the unique identifier for this tool call event.
	ID string `json:"id"`
	// Name is the name of the tool requested by the model.
	Name string `json:"name"`
	// Arguments is the JSON-encoded string of arguments provided by the model.
	Arguments string `json:"arguments"`
}

// UnmarshalArgs unmarshals the tool call's JSON arguments into the target destination.
func (tc ToolCall) UnmarshalArgs(target any) error {
	return json.Unmarshal([]byte(tc.Arguments), target)
}

// ToolDefinition describes a tool schema exposed to the model for function calling.
type ToolDefinition struct {
	// Name is the unique name of the tool.
	Name string `json:"name"`
	// Description explains what the tool does and when it should be invoked.
	Description string `json:"description"`
	// Parameters specifies the JSON Schema defining the expected inputs.
	Parameters any `json:"parameters,omitempty"`
}
