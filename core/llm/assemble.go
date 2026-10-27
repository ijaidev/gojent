package llm

import (
	"encoding/json"
	"fmt"
)

// Assembler turns canonical stream events into one message. Part indices are
// authoritative: index 0 is a valid part index.
type Assembler struct {
	role Role

	parts       []Part
	started     map[int]bool
	ended       map[int]bool
	messageOpen bool

	id              string
	model           string
	status          MessageStatus
	finishReason    FinishReason
	rawFinishReason string
	usage           Usage
	provider        ProviderData

	closed bool
}

// NewAssembler creates an assembler. An empty role means assistant.
func NewAssembler(role Role) *Assembler {
	if role == "" {
		role = RoleAssistant
	}
	return &Assembler{
		role:    role,
		started: make(map[int]bool),
		ended:   make(map[int]bool),
	}
}

// Apply consumes one event. Invalid indices and duplicate starts fail.
func (a *Assembler) Apply(event StreamEvent) error {
	event = normalizeStreamEvent(event)
	if event == nil {
		return fmt.Errorf("%w: nil stream event", ErrInvalidPart)
	}
	if a.closed {
		return fmt.Errorf("%w: event after MessageEnd: %T", ErrInvalidMessage, event)
	}

	switch e := event.(type) {
	case MessageStart:
		return a.applyMessageStart(e)
	case PartStart:
		return a.applyPartStart(e)
	case PartDelta:
		return a.applyPartDelta(e)
	case PartEnd:
		return a.applyPartEnd(e)
	case MessageEnd:
		return a.applyMessageEnd(e)
	case ProviderEvent:
		return a.applyProviderEvent(e)
	default:
		return fmt.Errorf("%w: unknown stream event %T", ErrInvalidMessage, event)
	}
}

func (a *Assembler) applyMessageStart(event MessageStart) error {
	if a.messageOpen {
		return fmt.Errorf("%w: duplicate MessageStart", ErrInvalidMessage)
	}
	a.messageOpen = true
	a.id = event.ID
	a.model = event.Model
	return nil
}

func (a *Assembler) applyPartStart(event PartStart) error {
	if event.Index < 0 {
		return fmt.Errorf("%w: negative part index %d", ErrInvalidPart, event.Index)
	}
	if event.Part == nil {
		return fmt.Errorf("%w: PartStart at %d has no part", ErrInvalidPart, event.Index)
	}
	if a.started[event.Index] {
		return fmt.Errorf("%w: duplicate PartStart at index %d", ErrInvalidPart, event.Index)
	}
	a.grow(event.Index)
	if existing := a.parts[event.Index]; existing != nil {
		if err := mergePart(existing, event.Part); err != nil {
			return fmt.Errorf("part %d: %w", event.Index, err)
		}
	} else {
		a.parts[event.Index] = event.Part
	}
	a.started[event.Index] = true
	return nil
}

func (a *Assembler) applyPartDelta(event PartDelta) error {
	if event.Index < 0 {
		return fmt.Errorf("%w: negative part index %d", ErrInvalidPart, event.Index)
	}
	if event.Kind == "" {
		return fmt.Errorf("%w: delta at %d has no kind", ErrInvalidPart, event.Index)
	}
	if a.ended[event.Index] {
		return fmt.Errorf("%w: delta after PartEnd at index %d", ErrInvalidPart, event.Index)
	}

	part, err := a.ensurePart(event.Index, event.Kind)
	if err != nil {
		return err
	}

	switch p := part.(type) {
	case *TextPart:
		if event.Call != nil {
			return fmt.Errorf("%w: text delta at %d has tool-call payload", ErrInvalidPart, event.Index)
		}
		if event.Replace {
			p.Text = event.Text
		} else {
			p.Text += event.Text
		}
	case *ReasoningPart:
		if event.Call != nil {
			return fmt.Errorf("%w: reasoning delta at %d has tool-call payload", ErrInvalidPart, event.Index)
		}
		if event.Replace {
			p.Text = event.Text
		} else {
			p.Text += event.Text
		}
	case *RefusalPart:
		if event.Call != nil {
			return fmt.Errorf("%w: refusal delta at %d has tool-call payload", ErrInvalidPart, event.Index)
		}
		if event.Replace {
			p.Text = event.Text
		} else {
			p.Text += event.Text
		}
	case *ToolCallPart:
		if event.Text != "" {
			return fmt.Errorf("%w: tool-call delta at %d has text", ErrInvalidPart, event.Index)
		}
		if err := applyToolCallDelta(p, event); err != nil {
			return fmt.Errorf("part %d: %w", event.Index, err)
		}
	default:
		return fmt.Errorf("%w: %q cannot be streamed with deltas", ErrInvalidPart, event.Kind)
	}

	return mergeProvider(part, event.Provider)
}

func applyToolCallDelta(part *ToolCallPart, event PartDelta) error {
	delta := event.Call
	if delta == nil {
		return fmt.Errorf("%w: tool call delta has no call payload", ErrInvalidToolCall)
	}
	if delta.ItemID != "" {
		part.ItemID = delta.ItemID
	}
	if delta.CallID != "" {
		part.CallID = delta.CallID
	}
	if delta.Name != "" {
		part.Name = delta.Name
	}
	if delta.InputSnapshot != nil {
		part.Input = append(part.Input[:0], delta.InputSnapshot...)
		return nil
	}
	if delta.InputFragment == "" {
		return nil
	}
	if event.Replace {
		part.Input = append(part.Input[:0], delta.InputFragment...)
		return nil
	}
	part.Input = append(part.Input, delta.InputFragment...)
	return nil
}

func (a *Assembler) applyPartEnd(event PartEnd) error {
	if event.Index < 0 {
		return fmt.Errorf("%w: negative part index %d", ErrInvalidPart, event.Index)
	}
	if a.ended[event.Index] {
		return fmt.Errorf("%w: duplicate PartEnd at index %d", ErrInvalidPart, event.Index)
	}
	if event.Index >= len(a.parts) || a.parts[event.Index] == nil {
		return fmt.Errorf("%w: PartEnd for unknown index %d", ErrInvalidPart, event.Index)
	}
	a.ended[event.Index] = true
	return nil
}

func (a *Assembler) applyMessageEnd(event MessageEnd) error {
	if event.Status != "" && !event.Status.valid() {
		return fmt.Errorf("%w: status %q", ErrInvalidMessage, event.Status)
	}
	a.status = event.Status
	a.finishReason = event.FinishReason
	a.rawFinishReason = event.RawFinishReason
	a.usage = event.Usage
	a.closed = true
	return nil
}

func (a *Assembler) applyProviderEvent(event ProviderEvent) error {
	if event.Provider == "" {
		return fmt.Errorf("%w: provider event has no provider", ErrInvalidPart)
	}
	if len(event.Raw) == 0 {
		return nil
	}
	if a.provider == nil {
		a.provider = make(ProviderData)
	}
	if err := a.provider.Set(event.Provider, event.Raw); err != nil {
		return err
	}
	return nil
}

func (a *Assembler) ensurePart(index int, kind PartType) (Part, error) {
	a.grow(index)
	part := a.parts[index]
	if part == nil {
		created, err := newStreamPart(kind)
		if err != nil {
			return nil, err
		}
		a.parts[index] = created
		return created, nil
	}
	if part.PartType() != kind {
		return nil, fmt.Errorf("%w: delta kind %q does not match part %q at index %d", ErrInvalidPart, kind, part.PartType(), index)
	}
	return part, nil
}

func (a *Assembler) grow(index int) {
	for len(a.parts) <= index {
		a.parts = append(a.parts, nil)
	}
}

// Finish returns the assembled response. It requires a terminal MessageEnd and
// at least one non-empty part.
func (a *Assembler) Finish() (*Response, error) {
	if !a.closed {
		return nil, fmt.Errorf("%w: stream ended without MessageEnd", ErrInvalidMessage)
	}

	parts := make([]Part, 0, len(a.parts))
	for i, part := range a.parts {
		if part == nil {
			return nil, fmt.Errorf("%w: missing part at index %d", ErrInvalidMessage, i)
		}
		if isEmptyStreamPart(part) {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: stream produced no content", ErrInvalidMessage)
	}

	status := a.status
	if status == "" {
		status = MessageStatusCompleted
	}
	message := Message{
		Role:     a.role,
		Content:  parts,
		Status:   status,
		Provider: a.provider.Clone(),
	}
	if err := message.Validate(); err != nil {
		return nil, err
	}
	return &Response{
		ID:              a.id,
		Model:           a.model,
		Message:         message,
		Status:          status,
		FinishReason:    a.finishReason,
		RawFinishReason: a.rawFinishReason,
		Usage:           a.usage,
	}, nil
}

// normalizeStreamEvent converts pointer events to their value form so the
// assembler has one type switch.
func normalizeStreamEvent(event StreamEvent) StreamEvent {
	switch e := event.(type) {
	case *MessageStart:
		if e == nil {
			return nil
		}
		return *e
	case *PartStart:
		if e == nil {
			return nil
		}
		return *e
	case *PartDelta:
		if e == nil {
			return nil
		}
		return *e
	case *PartEnd:
		if e == nil {
			return nil
		}
		return *e
	case *MessageEnd:
		if e == nil {
			return nil
		}
		return *e
	case *ProviderEvent:
		if e == nil {
			return nil
		}
		return *e
	default:
		return event
	}
}

func newStreamPart(kind PartType) (Part, error) {
	switch kind {
	case PartTypeText:
		return NewTextPart(""), nil
	case PartTypeReasoning:
		return NewReasoningPart(""), nil
	case PartTypeToolCall:
		return NewToolCallPart(ToolCall{}), nil
	case PartTypeRefusal:
		return NewRefusalPart(""), nil
	default:
		return nil, fmt.Errorf("%w: %q cannot be streamed", ErrInvalidPart, kind)
	}
}

// mergePart fills missing canonical fields of dst from src. It is used when a
// PartStart arrives after deltas already created a provisional part.
func mergePart(dst, src Part) error {
	if dst.PartType() != src.PartType() {
		return fmt.Errorf("%w: PartStart type %q does not match existing %q", ErrInvalidPart, src.PartType(), dst.PartType())
	}
	switch d := dst.(type) {
	case *TextPart:
		if d.Text == "" {
			d.Text = src.(*TextPart).Text
		}
	case *ReasoningPart:
		s := src.(*ReasoningPart)
		if d.Text == "" {
			d.Text = s.Text
		}
		if len(d.Summary) == 0 {
			d.Summary = append([]string(nil), s.Summary...)
		}
		d.Redacted = d.Redacted || s.Redacted
	case *FilePart:
		s := src.(*FilePart)
		if len(d.Data) == 0 && d.URL == "" && d.FileID == "" {
			*d = *s
		}
	case *ToolCallPart:
		s := src.(*ToolCallPart)
		if d.ItemID == "" {
			d.ItemID = s.ItemID
		}
		if d.CallID == "" {
			d.CallID = s.CallID
		}
		if d.Name == "" {
			d.Name = s.Name
		}
		if d.Kind == "" {
			d.Kind = s.Kind
		}
		if len(d.Input) == 0 {
			d.Input = append([]byte(nil), s.Input...)
		}
		if d.Status == "" {
			d.Status = s.Status
		}
	case *RefusalPart:
		if d.Text == "" {
			d.Text = src.(*RefusalPart).Text
		}
	case *ToolResultPart:
		return fmt.Errorf("%w: tool results are not streamed", ErrInvalidPart)
	}
	return mergeProviderFrom(dst, src)
}

func mergeProviderFrom(dst, src Part) error {
	if src.Meta() == nil || len(src.Meta().Provider) == 0 {
		return nil
	}
	if dst.Meta().Provider == nil {
		dst.Meta().Provider = make(ProviderData)
	}
	for key, raw := range src.Meta().Provider {
		dst.Meta().Provider[key] = append(json.RawMessage(nil), raw...)
	}
	return nil
}

func mergeProvider(part Part, provider ProviderData) error {
	if len(provider) == 0 {
		return nil
	}
	if part.Meta().Provider == nil {
		part.Meta().Provider = make(ProviderData)
	}
	for key, raw := range provider {
		part.Meta().Provider[key] = append(json.RawMessage(nil), raw...)
	}
	return nil
}

// isEmptyStreamPart reports whether an accumulated part carries no content and
// no provider metadata, so it can be dropped at Finish.
func isEmptyStreamPart(part Part) bool {
	if len(part.Meta().Provider) > 0 {
		return false
	}
	switch p := part.(type) {
	case *TextPart:
		return p.Text == ""
	case *ReasoningPart:
		return p.Text == "" && len(p.Summary) == 0 && !p.Redacted
	case *RefusalPart:
		return p.Text == ""
	default:
		return false
	}
}
