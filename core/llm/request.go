package llm

import "fmt"

// StateMode selects how a request references prior model state.
type StateMode string

const (
	// StateModeReplay sends the full canonical transcript on every call.
	StateModeReplay StateMode = "replay"
	// StateModeServer lets the provider keep state and sends only new input.
	StateModeServer StateMode = "server"
)

// State describes conversation state handling. The zero value is replay mode;
// server state is always explicit, never inferred from PreviousID (D13).
type State struct {
	// Mode is replay (default) or server.
	Mode StateMode `json:"mode,omitempty"`
	// PreviousID is the provider response or interaction ID to continue from.
	PreviousID string `json:"previous_id,omitempty"`
	// Store asks the provider to retain this response when supported.
	Store *bool `json:"store,omitempty"`
	// Provider carries adapter-specific state options.
	Provider ProviderData `json:"provider,omitempty"`
}

// EffectiveMode returns Mode, defaulting to replay.
func (s State) EffectiveMode() StateMode {
	if s.Mode == "" {
		return StateModeReplay
	}
	return s.Mode
}

// Validate checks the mode.
func (s State) Validate() error {
	switch s.EffectiveMode() {
	case StateModeReplay, StateModeServer:
		return nil
	default:
		return fmt.Errorf("%w: state mode %q", ErrUnsupported, s.Mode)
	}
}

// Request contains the provider-neutral input for one model call.
type Request struct {
	Messages        []Message        `json:"messages,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      ToolChoice       `json:"tool_choice,omitzero"`
	State           State            `json:"state,omitzero"`
	Reasoning       *ReasoningConfig `json:"reasoning,omitempty"`
	ResponseFormat  *ResponseFormat  `json:"response_format,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	Stop            []string         `json:"stop,omitempty"`
	ProviderOptions ProviderData     `json:"provider_options,omitempty"`
}

// Validate checks state and cross-message tool pairing.
func (r *Request) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: nil request", ErrInvalidMessage)
	}
	if err := r.State.Validate(); err != nil {
		return err
	}
	return ValidateTranscript(r.Messages)
}

// Clone returns a deep copy of the request.
func (r *Request) Clone() *Request {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Messages = CloneMessages(r.Messages)
	clone.Tools = cloneToolDefinitions(r.Tools)
	clone.State = r.State.clone()
	clone.Reasoning = r.Reasoning.Clone()
	clone.ResponseFormat = r.ResponseFormat.Clone()
	clone.Temperature = clonePtr(r.Temperature)
	clone.MaxTokens = clonePtr(r.MaxTokens)
	clone.TopP = clonePtr(r.TopP)
	clone.Stop = append([]string(nil), r.Stop...)
	clone.ProviderOptions = r.ProviderOptions.Clone()
	return &clone
}

func (s State) clone() State {
	clone := s
	clone.Store = clonePtr(s.Store)
	clone.Provider = s.Provider.Clone()
	return clone
}

func cloneToolDefinitions(definitions []ToolDefinition) []ToolDefinition {
	if len(definitions) == 0 {
		return nil
	}
	out := make([]ToolDefinition, len(definitions))
	for i, definition := range definitions {
		out[i] = definition
		out[i].Parameters = cloneSchema(definition.Parameters)
		out[i].Provider = definition.Provider.Clone()
	}
	return out
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
