package llm

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// FormatVersion is the message-model version written by MarshalMessage.
// Persisted messages written by another version must be rejected, not
// guessed at.
const FormatVersion = 2

func decodeParts(raw []json.RawMessage) ([]Part, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Part, 0, len(raw))
	for i, item := range raw {
		part, err := decodePart(item)
		if err != nil {
			return nil, fmt.Errorf("content[%d]: %w", i, err)
		}
		out = append(out, part)
	}
	return out, nil
}

// decodePart dispatches on the JSON type discriminator. Unknown types fail
// closed.
func decodePart(raw json.RawMessage) (Part, error) {
	var header struct {
		Type PartType `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPart, err)
	}

	var part Part
	switch header.Type {
	case PartTypeText:
		part = new(TextPart)
	case PartTypeReasoning:
		part = new(ReasoningPart)
	case PartTypeFile:
		part = new(FilePart)
	case PartTypeToolCall:
		part = new(ToolCallPart)
	case PartTypeToolResult:
		part = new(ToolResultPart)
	case PartTypeRefusal:
		part = new(RefusalPart)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownPartType, header.Type)
	}

	if err := json.Unmarshal(raw, part); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrInvalidPart, header.Type, err)
	}
	if part.PartType() != header.Type {
		return nil, fmt.Errorf("%w: decoded %q as %q", ErrInvalidPart, header.Type, part.PartType())
	}
	if call, ok := part.(*ToolCallPart); ok {
		call.normalize()
	}
	return part, nil
}

// UnmarshalJSON decodes a message and its ordered parts. Unknown part types
// fail closed.
func (m *Message) UnmarshalJSON(data []byte) error {
	var wire struct {
		Role     Role              `json:"role"`
		Content  []json.RawMessage `json:"content"`
		ItemID   string            `json:"item_id"`
		Status   MessageStatus     `json:"status"`
		Phase    string            `json:"phase"`
		Name     string            `json:"name"`
		Provider ProviderData      `json:"provider"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	content, err := decodeParts(wire.Content)
	if err != nil {
		return err
	}
	*m = Message{
		Role:     wire.Role,
		Content:  content,
		ItemID:   wire.ItemID,
		Status:   wire.Status,
		Phase:    wire.Phase,
		Name:     wire.Name,
		Provider: wire.Provider,
	}
	return nil
}

// MarshalMessage encodes a message with the canonical format_version prefix.
// Use json.Marshal for transport payloads that do not persist the transcript.
func MarshalMessage(m Message) ([]byte, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	fields["format_version"] = json.RawMessage(strconv.Itoa(FormatVersion))
	return json.Marshal(fields)
}

// UnmarshalMessage decodes a message written by MarshalMessage. A missing or
// different format_version is an error, never a best-effort parse.
func UnmarshalMessage(data []byte) (Message, error) {
	var header struct {
		FormatVersion int `json:"format_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Message{}, err
	}
	if header.FormatVersion != FormatVersion {
		return Message{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedFormatVersion, header.FormatVersion, FormatVersion)
	}
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Message{}, err
	}
	return message, nil
}
