package storage

import (
	"maps"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

// Message represents a durable conversation message stored within a thread.
// Content is multipart by default, containing one or more multimodal parts.
type Message struct {
	// ID is the unique identifier for this message.
	ID string `json:"id"`
	// ThreadID references the parent conversation thread.
	ThreadID string `json:"thread_id"`
	// ResourceID is an optional identifier for the owner (user, tenant, or agent).
	ResourceID string `json:"resource_id,omitempty"`
	// Role identifies the author role (system, user, assistant, tool).
	Role llm.Role `json:"role"`
	// Content contains the multimodal parts of the message.
	Content []llm.Part `json:"content"`
	// Name specifies an author name or tool name.
	Name string `json:"name,omitempty"`
	// ToolCalls contains model-requested tool calls (for assistant messages).
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID specifies the identifier of the tool call this message responds to (for tool messages).
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CreatedAt is the timestamp when the message was recorded.
	CreatedAt time.Time `json:"created_at"`
	// Metadata contains arbitrary custom metadata attached to this message.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Raw holds provider-specific payloads (e.g. Gemini thought signatures or OpenAI annotations).
	Raw any `json:"raw,omitempty"`
}

// Text returns the aggregated textual output of all text parts in the message.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range m.Content {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// Clone returns a deep copy of the Message to prevent mutation races.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	clone := *m
	if m.Content != nil {
		clone.Content = make([]llm.Part, len(m.Content))
		copy(clone.Content, m.Content)
	}
	if m.ToolCalls != nil {
		clone.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
		copy(clone.ToolCalls, m.ToolCalls)
	}
	if m.Metadata != nil {
		clone.Metadata = make(map[string]any, len(m.Metadata))
		maps.Copy(clone.Metadata, m.Metadata)
	}
	return &clone
}

// ToLLMMessage converts a storage Message to an execution llm.Message.
func (m *Message) ToLLMMessage() llm.Message {
	if m == nil {
		return llm.Message{}
	}
	return llm.Message{
		Role:       m.Role,
		Content:    m.Content,
		Name:       m.Name,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Raw:        m.Raw,
	}
}

// NewMessage constructs a new storage Message with the given thread ID, role, and text content.
func NewMessage(threadID string, role llm.Role, content string) *Message {
	return &Message{
		ThreadID:  threadID,
		Role:      role,
		Content:   []llm.Part{llm.TextPart(content)},
		CreatedAt: time.Now().UTC(),
	}
}

// NewMessageWithParts constructs a new storage Message with explicit multimodal parts.
func NewMessageWithParts(threadID string, role llm.Role, parts ...llm.Part) *Message {
	return &Message{
		ThreadID:  threadID,
		Role:      role,
		Content:   parts,
		CreatedAt: time.Now().UTC(),
	}
}

// NewMessageFromLLM converts an llm.Message into a storage Message assigned to a thread.
func NewMessageFromLLM(threadID string, llmMsg llm.Message) *Message {
	return &Message{
		ThreadID:   threadID,
		Role:       llmMsg.Role,
		Content:    llmMsg.Content,
		Name:       llmMsg.Name,
		ToolCalls:  llmMsg.ToolCalls,
		ToolCallID: llmMsg.ToolCallID,
		Raw:        llmMsg.Raw,
		CreatedAt:  time.Now().UTC(),
	}
}

// ToLLMMessages converts a slice of storage Messages into a slice of execution llm.Messages.
func ToLLMMessages(msgs []*Message) []llm.Message {
	if msgs == nil {
		return nil
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		if m != nil {
			out[i] = m.ToLLMMessage()
		}
	}
	return out
}

// MessageFilter specifies criteria for querying messages within a thread or across threads.
type MessageFilter struct {
	// ThreadID specifies the thread whose messages should be retrieved.
	ThreadID string `json:"thread_id,omitempty"`
	// ResourceID filters messages by resource owner.
	ResourceID string `json:"resource_id,omitempty"`
	// Role optionally filters messages by role (e.g. only user or assistant messages).
	Role llm.Role `json:"role,omitempty"`
	// Since filters messages created on or after this timestamp.
	Since *time.Time `json:"since,omitempty"`
	// Until filters messages created on or before this timestamp.
	Until *time.Time `json:"until,omitempty"`
	// Limit sets the maximum number of messages to return (0 means unlimited / default).
	Limit int `json:"limit,omitempty"`
	// Offset sets the number of initial messages to skip for pagination.
	Offset int `json:"offset,omitempty"`
	// SortOrder specifies whether messages are returned in ascending or descending chronological order (default: SortAsc).
	SortOrder SortOrder `json:"sort_order,omitempty"`
}
