package llm

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Role identifies a message author.
type Role string

const (
	// RoleSystem carries high-level instructions.
	RoleSystem Role = "system"
	// RoleUser carries human or application input.
	RoleUser Role = "user"
	// RoleAssistant carries model output.
	RoleAssistant Role = "assistant"
	// RoleTool carries a tool result.
	RoleTool Role = "tool"
)

// Message is one turn in a conversation.
//
// Content is the canonical, provider-neutral representation. Tool results set
// ToolCallID and may set IsError and Data. ProviderData holds opaque replay
// state and never replaces canonical content.
type Message struct {
	Role       Role       `json:"role"`
	Content    []Part     `json:"content,omitempty"`
	Thinking   string     `json:"thinking,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// IsError marks a tool result that represents a failure.
	IsError bool `json:"is_error,omitempty"`
	// Data holds structured tool output as JSON, when available.
	Data json.RawMessage `json:"data,omitempty"`
	// ProviderData holds opaque provider-specific replay state.
	ProviderData *ProviderData `json:"provider_data,omitempty"`
}

// Text returns the aggregated textual content of all text parts.
func (m Message) Text() string {
	return TextFromParts(m.Content)
}

// TextFromParts joins non-empty text parts with newlines.
func TextFromParts(parts []Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// NonEmptyParts returns a copy of parts with empty text parts removed.
func NonEmptyParts(parts []Part) []Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		if p.Type == PartTypeText && p.Text == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WithThinking returns a copy with the given reasoning trace.
func (m Message) WithThinking(thinking string) Message {
	m.Thinking = thinking
	return m
}

// Clone returns a deep copy of the message.
func (m Message) Clone() Message {
	clone := m
	if len(m.Content) > 0 {
		clone.Content = make([]Part, len(m.Content))
		for i, part := range m.Content {
			clone.Content[i] = part
			clone.Content[i].Data = slices.Clone(part.Data)
		}
	}
	clone.ToolCalls = slices.Clone(m.ToolCalls)
	clone.Data = append(json.RawMessage(nil), m.Data...)
	clone.ProviderData = m.ProviderData.Clone()
	return clone
}

// MarshalJSON omits empty text parts so persisted transcripts always replay.
func (m Message) MarshalJSON() ([]byte, error) {
	type wireMessage struct {
		Role         Role            `json:"role"`
		Content      []Part          `json:"content,omitempty"`
		Thinking     string          `json:"thinking,omitempty"`
		Name         string          `json:"name,omitempty"`
		ToolCalls    []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID   string          `json:"tool_call_id,omitempty"`
		IsError      bool            `json:"is_error,omitempty"`
		Data         json.RawMessage `json:"data,omitempty"`
		ProviderData *ProviderData   `json:"provider_data,omitempty"`
	}
	return json.Marshal(wireMessage{
		Role:         m.Role,
		Content:      NonEmptyParts(m.Content),
		Thinking:     m.Thinking,
		Name:         m.Name,
		ToolCalls:    m.ToolCalls,
		ToolCallID:   m.ToolCallID,
		IsError:      m.IsError,
		Data:         m.Data,
		ProviderData: m.ProviderData,
	})
}

// UnmarshalJSON accepts either string content or an array of parts.
func (m *Message) UnmarshalJSON(data []byte) error {
	type wireMessage struct {
		Role         Role            `json:"role"`
		Content      json.RawMessage `json:"content"`
		Thinking     string          `json:"thinking,omitempty"`
		Name         string          `json:"name,omitempty"`
		ToolCalls    []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID   string          `json:"tool_call_id,omitempty"`
		IsError      bool            `json:"is_error,omitempty"`
		Data         json.RawMessage `json:"data,omitempty"`
		ProviderData *ProviderData   `json:"provider_data,omitempty"`
	}
	var raw wireMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Role = raw.Role
	m.Thinking = raw.Thinking
	m.Name = raw.Name
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	m.IsError = raw.IsError
	m.Data = raw.Data
	m.ProviderData = raw.ProviderData
	m.Content = nil

	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}

	var parts []Part
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.Content = NonEmptyParts(parts)
		return nil
	}

	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil {
		if text != "" {
			m.Content = []Part{TextPart(text)}
		}
		return nil
	}

	return fmt.Errorf("llm: invalid content format in message JSON")
}
