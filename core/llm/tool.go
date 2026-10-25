package llm

import (
	"encoding/json"
	"fmt"
)

// ToolKind classifies who executes a tool call (D10).
type ToolKind string

const (
	// ToolKindFunction is a client-side function executed by the runner.
	ToolKindFunction ToolKind = "function"
	// ToolKindCustom is a client-side free-form tool executed by the runner.
	ToolKindCustom ToolKind = "custom"
	// ToolKindServer is a provider-hosted tool. The runner records it and
	// never executes it.
	ToolKindServer ToolKind = "server"
	// ToolKindMCP is a tool exposed by an MCP server.
	ToolKindMCP ToolKind = "mcp"
)

func (k ToolKind) valid() bool {
	switch k {
	case ToolKindFunction, ToolKindCustom, ToolKindServer, ToolKindMCP:
		return true
	default:
		return false
	}
}

// ToolStatus reports call or result progress.
type ToolStatus string

const (
	// ToolStatusInProgress means the call or result is still streaming.
	ToolStatusInProgress ToolStatus = "in_progress"
	// ToolStatusCompleted means the call or result finished successfully.
	ToolStatusCompleted ToolStatus = "completed"
	// ToolStatusIncomplete means the provider stopped before completion.
	ToolStatusIncomplete ToolStatus = "incomplete"
	// ToolStatusError means the tool returned an error.
	ToolStatusError ToolStatus = "error"
	// ToolStatusDenied means a policy rejected the call.
	ToolStatusDenied ToolStatus = "denied"
	// ToolStatusInputRequired means execution is paused for user input.
	ToolStatusInputRequired ToolStatus = "input_required"
)

func (s ToolStatus) valid() bool {
	switch s {
	case ToolStatusInProgress, ToolStatusCompleted, ToolStatusIncomplete,
		ToolStatusError, ToolStatusDenied, ToolStatusInputRequired:
		return true
	default:
		return false
	}
}

// ToolCall is a model request to invoke a tool.
//
// ItemID is the provider's item identity (for example an OpenAI Responses
// item). CallID is the correlation ID used by results. They are different
// fields and must not be conflated (D7).
type ToolCall struct {
	// ItemID identifies the provider item that carried the call.
	ItemID string `json:"item_id,omitempty"`
	// CallID correlates the call with its result.
	CallID string `json:"call_id"`
	// Name is the tool name requested by the model.
	Name string `json:"name"`
	// Kind selects the execution path. Empty means function.
	Kind ToolKind `json:"kind,omitempty"`
	// Input is the JSON value passed to the tool. For custom tools it is a
	// JSON string; for function tools it is a JSON object (D8).
	Input json.RawMessage `json:"input,omitempty"`
	// Status is the provider-reported call status, when it reports one.
	Status ToolStatus `json:"status,omitempty"`
}

// EffectiveKind returns Kind, defaulting to a function call.
func (c ToolCall) EffectiveKind() ToolKind {
	if c.Kind == "" {
		return ToolKindFunction
	}
	return c.Kind
}

// UnmarshalInput decodes the call input into target. Empty input is a no-op.
func (c ToolCall) UnmarshalInput(target any) error {
	if len(c.Input) == 0 {
		return nil
	}
	if err := json.Unmarshal(c.Input, target); err != nil {
		return fmt.Errorf("llm: decode input for tool %q: %w", c.Name, err)
	}
	return nil
}

func (c *ToolCall) normalize() {
	if c.Kind == "" {
		c.Kind = ToolKindFunction
	}
}

func (c ToolCall) validate() error {
	if c.Name == "" {
		return fmt.Errorf("%w: missing tool name", ErrInvalidToolCall)
	}
	if c.CallID == "" {
		return fmt.Errorf("%w: missing call id for %q", ErrInvalidToolCall, c.Name)
	}
	if c.Kind != "" && !c.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidToolCall, c.Kind)
	}
	if c.Status != "" && !c.Status.valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidToolCall, c.Status)
	}
	if len(c.Input) > 0 && !json.Valid(c.Input) {
		return fmt.Errorf("%w: input for %q is not valid JSON", ErrInvalidToolCall, c.Name)
	}
	return nil
}

// ToolResult is the output of one tool invocation.
type ToolResult struct {
	// CallID is the correlation ID copied from the call.
	CallID string `json:"call_id"`
	// Name is the tool name, when the provider reports it.
	Name string `json:"name,omitempty"`
	// Kind is the execution path of the originating call.
	Kind ToolKind `json:"kind,omitempty"`
	// Status reports success, failure, or pending input.
	Status ToolStatus `json:"status,omitempty"`
	// Content is ordered text and file output. Nested tool calls and results
	// are not allowed.
	Content []Part `json:"content,omitempty"`
	// Structured holds a JSON payload for structured-output tools.
	Structured json.RawMessage `json:"structured,omitempty"`
	// InputRequests holds JSON describing input needed to continue, when
	// Status is ToolStatusInputRequired.
	InputRequests json.RawMessage `json:"input_requests,omitempty"`
}

// Text joins the text parts of the result.
func (r ToolResult) Text() string {
	return TextFromParts(r.Content)
}

// EffectiveKind returns Kind, defaulting to a function call.
func (r ToolResult) EffectiveKind() ToolKind {
	if r.Kind == "" {
		return ToolKindFunction
	}
	return r.Kind
}

func (r *ToolResult) normalize() {
	if r.Kind == "" {
		r.Kind = ToolKindFunction
	}
	if r.Status == "" {
		r.Status = ToolStatusCompleted
	}
}

func (r ToolResult) validate() error {
	if r.CallID == "" {
		return fmt.Errorf("%w: missing call id", ErrInvalidToolResult)
	}
	if r.Kind != "" && !r.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidToolResult, r.Kind)
	}
	if !r.Status.valid() {
		return fmt.Errorf("%w: invalid status %q", ErrInvalidToolResult, r.Status)
	}
	if len(r.Structured) > 0 && !json.Valid(r.Structured) {
		return fmt.Errorf("%w: structured output is not valid JSON", ErrInvalidToolResult)
	}
	if len(r.InputRequests) > 0 && !json.Valid(r.InputRequests) {
		return fmt.Errorf("%w: input requests are not valid JSON", ErrInvalidToolResult)
	}
	if r.Status == ToolStatusInputRequired && len(r.InputRequests) == 0 {
		return fmt.Errorf("%w: input_required without input requests", ErrInvalidToolResult)
	}
	return validateToolResultContent(r.Content)
}

// ToolCallPart is a tool call in an assistant message.
type ToolCallPart struct {
	partBase
	ToolCall
}

// NewToolCallPart creates a tool call part, defaulting the kind to function.
func NewToolCallPart(call ToolCall) *ToolCallPart {
	call.normalize()
	return &ToolCallPart{
		partBase: partBase{Type: PartTypeToolCall},
		ToolCall: call,
	}
}

func (p *ToolCallPart) validate() error {
	if err := p.partBase.validateType(PartTypeToolCall); err != nil {
		return err
	}
	return p.ToolCall.validate()
}

// ToolResultPart is a tool result in a user message (D5).
type ToolResultPart struct {
	partBase
	ToolResult
}

// NewToolResultPart creates a tool result part, defaulting kind and status.
func NewToolResultPart(result ToolResult) *ToolResultPart {
	result.normalize()
	return &ToolResultPart{
		partBase:   partBase{Type: PartTypeToolResult},
		ToolResult: result,
	}
}

// UnmarshalJSON decodes nested result content, which needs the sealed part
// dispatcher.
func (p *ToolResultPart) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type          PartType          `json:"type"`
		Provider      ProviderData      `json:"provider"`
		CallID        string            `json:"call_id"`
		Name          string            `json:"name"`
		Kind          ToolKind          `json:"kind"`
		Status        ToolStatus        `json:"status"`
		Content       []json.RawMessage `json:"content"`
		Structured    json.RawMessage   `json:"structured"`
		InputRequests json.RawMessage   `json:"input_requests"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	content, err := decodeParts(wire.Content)
	if err != nil {
		return err
	}
	result := ToolResult{
		CallID:        wire.CallID,
		Name:          wire.Name,
		Kind:          wire.Kind,
		Status:        wire.Status,
		Content:       content,
		Structured:    wire.Structured,
		InputRequests: wire.InputRequests,
	}
	result.normalize()
	p.partBase = partBase{Type: wire.Type, PartMeta: PartMeta{Provider: wire.Provider}}
	p.ToolResult = result
	return nil
}

func (p *ToolResultPart) validate() error {
	if err := p.partBase.validateType(PartTypeToolResult); err != nil {
		return err
	}
	return p.ToolResult.validate()
}

// ToolDefinition describes a tool exposed to the model.
type ToolDefinition struct {
	// Name is the unique tool name.
	Name string `json:"name"`
	// Description explains what the tool does and when to use it.
	Description string `json:"description"`
	// Parameters is the JSON Schema for the tool input.
	Parameters map[string]any `json:"parameters,omitempty"`
	// Strict asks the provider to enforce the schema when supported.
	Strict bool `json:"strict,omitempty"`
	// Kind classifies who executes the tool. Empty means function.
	Kind ToolKind `json:"kind,omitempty"`
	// Provider carries adapter configuration for hosted or server tools.
	Provider ProviderData `json:"provider,omitempty"`
}

// EffectiveKind returns Kind, defaulting to a function tool.
func (d ToolDefinition) EffectiveKind() ToolKind {
	if d.Kind == "" {
		return ToolKindFunction
	}
	return d.Kind
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
	// Name optionally forces a specific tool.
	Name string `json:"name,omitempty"`
}
