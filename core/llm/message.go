package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Role represents the role of a message participant.
type Role string

const (
	// RoleSystem is for high-level instructions directing model behavior.
	RoleSystem Role = "system"
	// RoleUser represents the end-user or human input.
	RoleUser Role = "user"
	// RoleAssistant represents responses from the model.
	RoleAssistant Role = "assistant"
	// RoleTool represents tool execution outputs returned to the model.
	RoleTool Role = "tool"
)

// Message represents a single chat message in a conversation.
// Content is multipart by default, representing one or more multimodal parts.
type Message struct {
	Role       Role       `json:"role"`
	Content    []Part     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Raw holds provider-specific message/content payload (e.g. Gemini thought signatures or OpenAI metadata).
	Raw any `json:"raw,omitempty"`
}

// Text returns the aggregated textual content of all text parts in the message.
func (m Message) Text() string {
	var sb strings.Builder
	for _, p := range m.Content {
		if p.Type == PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// WithThinking returns a copy of the message with the given reasoning/thinking trace.
func (m Message) WithThinking(thinking string) Message {
	m.Thinking = thinking
	return m
}

// UnmarshalJSON customizes JSON unmarshaling to accept either a string or an array of parts for Content.
func (m *Message) UnmarshalJSON(data []byte) error {
	type rawMessage struct {
		Role       Role            `json:"role"`
		Content    json.RawMessage `json:"content"`
		Thinking   string          `json:"thinking,omitempty"`
		Name       string          `json:"name,omitempty"`
		ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
		Raw        any             `json:"raw,omitempty"`
	}
	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Thinking = raw.Thinking
	m.Name = raw.Name
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	m.Raw = raw.Raw

	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		m.Content = nil
		return nil
	}

	var parts []Part
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.Content = parts
		return nil
	}

	var str string
	if err := json.Unmarshal(raw.Content, &str); err == nil {
		m.Content = []Part{TextPart(str)}
		return nil
	}

	return fmt.Errorf("llm: invalid content format in message JSON")
}
