package llm

import (
	"encoding/json"
	"fmt"
)

// ProviderData carries provider-specific state that must survive persistence
// so a transcript can be replayed exactly.
//
// ProviderData annotates canonical fields; it never replaces them. Adapters
// must read only their own key and must keep text, reasoning, tool calls, tool
// results, and files authoritative.
//
// Keys are provider identifiers such as "google", "anthropic", or "openai".
// Values are opaque JSON owned by the adapter.
type ProviderData map[string]json.RawMessage

// Set stores value under provider. It returns an error when the map is nil;
// use PartMeta.Set or allocate the map first.
func (p ProviderData) Set(provider string, value any) error {
	if p == nil {
		return fmt.Errorf("%w: cannot set %q on nil provider data", ErrNoProviderData, provider)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("llm: encode provider data %q: %w", provider, err)
	}
	p[provider] = raw
	return nil
}

// Get decodes the payload for provider into target. It returns
// ErrNoProviderData when the key is absent, including on a nil map.
func (p ProviderData) Get(provider string, target any) error {
	raw, ok := p[provider]
	if !ok || len(raw) == 0 {
		return fmt.Errorf("%w: provider %q", ErrNoProviderData, provider)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("llm: decode provider data %q: %w", provider, err)
	}
	return nil
}

// Raw returns the stored payload for provider without decoding it.
func (p ProviderData) Raw(provider string) (json.RawMessage, bool) {
	raw, ok := p[provider]
	return raw, ok
}

// Clone returns an independent copy. A nil or empty map returns nil.
func (p ProviderData) Clone() ProviderData {
	if len(p) == 0 {
		return nil
	}
	out := make(ProviderData, len(p))
	for key, raw := range p {
		out[key] = append(json.RawMessage(nil), raw...)
	}
	return out
}
