package llm

import "encoding/json"

// ProviderData carries provider-specific state that must survive persistence
// so a conversation can be replayed exactly.
//
// ProviderData annotates canonical messages; it never replaces their content.
// Adapters must keep the canonical text, tool calls, and parts authoritative
// and use ProviderData only for opaque replay state such as Gemini thought
// signatures, OpenAI reasoning identifiers, or Anthropic thinking blocks.
type ProviderData struct {
	// Provider identifies the adapter that produced the data, for example
	// "google" or "openai".
	Provider string `json:"provider"`
	// Version allows adapters to evolve their payload format.
	Version int `json:"version,omitempty"`
	// Data is an opaque JSON payload owned by the provider adapter.
	Data json.RawMessage `json:"data"`
}

// Clone returns an independent copy of the provider data.
func (p *ProviderData) Clone() *ProviderData {
	if p == nil {
		return nil
	}
	clone := *p
	clone.Data = append(json.RawMessage(nil), p.Data...)
	return &clone
}
