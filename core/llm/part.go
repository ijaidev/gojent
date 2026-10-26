package llm

import "fmt"

// PartType identifies a concrete content part.
type PartType string

const (
	// PartTypeText is plain text.
	PartTypeText PartType = "text"
	// PartTypeReasoning is model reasoning or a reasoning summary.
	PartTypeReasoning PartType = "reasoning"
	// PartTypeFile is one media or document payload. MIMEType decides how
	// providers encode it; there are no separate image/audio/video/document
	// part types (D2, D3).
	PartTypeFile PartType = "file"
	// PartTypeToolCall is a model request to invoke a tool.
	PartTypeToolCall PartType = "tool_call"
	// PartTypeToolResult is the output of a tool invocation.
	PartTypeToolResult PartType = "tool_result"
	// PartTypeRefusal is a model refusal to answer.
	PartTypeRefusal PartType = "refusal"
)

// Part is a closed sum type. The unexported method prevents implementations
// outside this package. Unknown JSON part types fail closed (D1, D18).
type Part interface {
	PartType() PartType
	Meta() *PartMeta
	isPart()
}

// PartMeta carries annotations shared by every part.
type PartMeta struct {
	// Provider holds per-part replay state keyed by provider. It annotates
	// canonical content and never replaces it (D11).
	Provider ProviderData `json:"provider,omitempty"`
}

// Set stores provider metadata, allocating the map when needed.
func (m *PartMeta) Set(provider string, value any) error {
	if m.Provider == nil {
		m.Provider = make(ProviderData)
	}
	return m.Provider.Set(provider, value)
}

// Get decodes provider metadata into target.
func (m *PartMeta) Get(provider string, target any) error {
	return m.Provider.Get(provider, target)
}

// partBase supplies the JSON discriminator and shared metadata. It is
// embedded by value so concrete parts can be marshaled directly.
type partBase struct {
	Type PartType `json:"type"`
	PartMeta
}

// PartType returns the concrete part type.
func (b partBase) PartType() PartType { return b.Type }

// Meta returns the part's mutable metadata.
func (b *partBase) Meta() *PartMeta { return &b.PartMeta }

func (partBase) isPart() {}

func (b partBase) validateType(want PartType) error {
	if b.Type != want {
		return fmt.Errorf("%w: type %q, want %q", ErrInvalidPart, b.Type, want)
	}
	return nil
}

// TextPart is canonical text.
type TextPart struct {
	partBase
	Text string `json:"text"`
}

// NewTextPart creates a text part.
func NewTextPart(text string) *TextPart {
	return &TextPart{partBase: partBase{Type: PartTypeText}, Text: text}
}

func (p *TextPart) validate() error {
	if err := p.validateType(PartTypeText); err != nil {
		return err
	}
	if p.Text == "" {
		return fmt.Errorf("%w: empty text", ErrInvalidPart)
	}
	return nil
}

// ReasoningPart is readable reasoning, a summary, or a redacted/encrypted
// reasoning payload. Provider signatures, encrypted content, and item IDs live
// in PartMeta.Provider under the provider key.
type ReasoningPart struct {
	partBase
	Text     string   `json:"text,omitempty"`
	Summary  []string `json:"summary,omitempty"`
	Redacted bool     `json:"redacted,omitempty"`
}

// NewReasoningPart creates a reasoning part with readable text.
func NewReasoningPart(text string) *ReasoningPart {
	return &ReasoningPart{partBase: partBase{Type: PartTypeReasoning}, Text: text}
}

func (p *ReasoningPart) validate() error {
	if err := p.validateType(PartTypeReasoning); err != nil {
		return err
	}
	if p.Text == "" && !p.Redacted && len(p.Summary) == 0 && len(p.Provider) == 0 {
		return fmt.Errorf("%w: empty reasoning", ErrInvalidPart)
	}
	return nil
}

// RefusalPart is an explicit model refusal.
type RefusalPart struct {
	partBase
	Text string `json:"text"`
}

// NewRefusalPart creates a refusal part.
func NewRefusalPart(text string) *RefusalPart {
	return &RefusalPart{partBase: partBase{Type: PartTypeRefusal}, Text: text}
}

func (p *RefusalPart) validate() error {
	if err := p.validateType(PartTypeRefusal); err != nil {
		return err
	}
	if p.Text == "" {
		return fmt.Errorf("%w: empty refusal", ErrInvalidPart)
	}
	return nil
}

// clonePart deep-copies one part. The type switch is exhaustive because Part
// is sealed; an unknown implementation is a programming error.
func clonePart(part Part) Part {
	switch p := part.(type) {
	case *TextPart:
		clone := *p
		clone.Provider = p.Provider.Clone()
		return &clone
	case *ReasoningPart:
		clone := *p
		clone.Summary = append([]string(nil), p.Summary...)
		clone.Provider = p.Provider.Clone()
		return &clone
	case *FilePart:
		clone := *p
		clone.Data = append([]byte(nil), p.Data...)
		clone.Provider = p.Provider.Clone()
		return &clone
	case *ToolCallPart:
		clone := *p
		clone.Input = append([]byte(nil), p.Input...)
		clone.Provider = p.Provider.Clone()
		return &clone
	case *ToolResultPart:
		clone := *p
		clone.Content = cloneParts(p.Content)
		clone.Structured = append([]byte(nil), p.Structured...)
		clone.InputRequests = append([]byte(nil), p.InputRequests...)
		clone.Provider = p.Provider.Clone()
		return &clone
	case *RefusalPart:
		clone := *p
		clone.Provider = p.Provider.Clone()
		return &clone
	default:
		panic(fmt.Sprintf("llm: unknown part implementation %T", part))
	}
}

func cloneParts(parts []Part) []Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]Part, len(parts))
	for i, part := range parts {
		out[i] = clonePart(part)
	}
	return out
}

// CloneParts returns a deep copy of parts. Empty and nil slices return nil.
func CloneParts(parts []Part) []Part {
	return cloneParts(parts)
}

// validatePart runs type-specific validation.
func validatePart(part Part) error {
	switch p := part.(type) {
	case *TextPart:
		return p.validate()
	case *ReasoningPart:
		return p.validate()
	case *FilePart:
		return p.validate()
	case *ToolCallPart:
		return p.validate()
	case *ToolResultPart:
		return p.validate()
	case *RefusalPart:
		return p.validate()
	default:
		return fmt.Errorf("%w: %T", ErrInvalidPart, part)
	}
}

// validateToolResultContent restricts nested results to text and files.
func validateToolResultContent(content []Part) error {
	for i, part := range content {
		switch p := part.(type) {
		case *TextPart:
			if err := p.validate(); err != nil {
				return fmt.Errorf("content[%d]: %w", i, err)
			}
		case *FilePart:
			if err := p.validate(); err != nil {
				return fmt.Errorf("content[%d]: %w", i, err)
			}
		default:
			return fmt.Errorf("%w: tool result content[%d] is %T, want text or file", ErrInvalidToolResult, i, part)
		}
	}
	return nil
}
