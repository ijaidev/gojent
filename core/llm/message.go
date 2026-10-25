package llm

import (
	"fmt"
	"slices"
	"strings"
)

// Role identifies a message author. Tool results are parts inside user
// messages; there is no tool role (D5).
type Role string

const (
	// RoleSystem carries high-level instructions.
	RoleSystem Role = "system"
	// RoleDeveloper carries developer instructions. Adapters hoist or
	// concatenate it with system instructions (D16).
	RoleDeveloper Role = "developer"
	// RoleUser carries human, application, or tool-result input.
	RoleUser Role = "user"
	// RoleAssistant carries model output.
	RoleAssistant Role = "assistant"
)

func (r Role) valid() bool {
	switch r {
	case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant:
		return true
	default:
		return false
	}
}

// MessageStatus reports whether a message is complete.
type MessageStatus string

const (
	// MessageStatusInProgress means the message is still streaming.
	MessageStatusInProgress MessageStatus = "in_progress"
	// MessageStatusCompleted means the message finished successfully.
	MessageStatusCompleted MessageStatus = "completed"
	// MessageStatusIncomplete means the provider stopped before completion.
	MessageStatusIncomplete MessageStatus = "incomplete"
	// MessageStatusFailed means the provider reported a failure.
	MessageStatusFailed MessageStatus = "failed"
)

func (s MessageStatus) valid() bool {
	switch s {
	case MessageStatusInProgress, MessageStatusCompleted, MessageStatusIncomplete, MessageStatusFailed:
		return true
	default:
		return false
	}
}

// Message is one turn in a conversation.
//
// Content is the only ordered content stream (D4). ItemID, Status, Phase, and
// Name are envelope metadata. Provider holds message-level metadata and never
// replaces canonical parts.
type Message struct {
	Role Role `json:"role"`
	// Content is the ordered canonical content.
	Content []Part `json:"content,omitempty"`
	// ItemID is the provider's item identity for this message.
	ItemID string `json:"item_id,omitempty"`
	// Status reports provider message status.
	Status MessageStatus `json:"status,omitempty"`
	// Phase is an OpenAI Responses phase such as "commentary" or
	// "final_answer".
	Phase string `json:"phase,omitempty"`
	// Name is an optional participant name.
	Name string `json:"name,omitempty"`
	// Provider holds message-level provider metadata.
	Provider ProviderData `json:"provider,omitempty"`
}

// Text joins all text parts with newlines.
func (m Message) Text() string {
	return TextFromParts(m.Content)
}

// ToolCalls returns the tool calls in content order.
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, part := range m.Content {
		if call, ok := part.(*ToolCallPart); ok {
			calls = append(calls, call.ToolCall)
		}
	}
	return calls
}

// ToolResults returns the tool results in content order.
func (m Message) ToolResults() []ToolResult {
	var results []ToolResult
	for _, part := range m.Content {
		if result, ok := part.(*ToolResultPart); ok {
			results = append(results, result.ToolResult)
		}
	}
	return results
}

// Validate checks role, content presence, and part placement. It does not
// check cross-message tool pairing; use ValidateTranscript for that.
func (m Message) Validate() error {
	if !m.Role.valid() {
		return fmt.Errorf("%w: role %q", ErrInvalidMessage, m.Role)
	}
	if m.Status != "" && !m.Status.valid() {
		return fmt.Errorf("%w: status %q", ErrInvalidMessage, m.Status)
	}
	if len(m.Content) == 0 {
		return fmt.Errorf("%w: empty content", ErrInvalidMessage)
	}
	for i, part := range m.Content {
		if part == nil {
			return fmt.Errorf("%w: nil part at %d", ErrInvalidMessage, i)
		}
		if err := validatePart(part); err != nil {
			return fmt.Errorf("content[%d]: %w", i, err)
		}
		if err := validatePlacement(m.Role, part); err != nil {
			return fmt.Errorf("content[%d]: %w", i, err)
		}
	}
	return nil
}

func validatePlacement(role Role, part Part) error {
	switch part.(type) {
	case *TextPart, *FilePart:
		return nil
	case *ReasoningPart, *ToolCallPart, *RefusalPart:
		if role != RoleAssistant {
			return fmt.Errorf("%w: %s part in %s message", ErrInvalidPart, part.PartType(), role)
		}
	case *ToolResultPart:
		if role != RoleUser {
			return fmt.Errorf("%w: tool result in %s message", ErrInvalidPart, role)
		}
	default:
		return fmt.Errorf("%w: %T", ErrInvalidPart, part)
	}
	return nil
}

// ValidateTranscript validates every message and the tool call/result
// pairing across the transcript.
//
// Every function, custom, or MCP call must have exactly one result, in the
// next user message. Server calls need no result because they are never
// executed locally. A transcript may end with unresolved calls: the runner is
// about to execute them.
func ValidateTranscript(messages []Message) error {
	pending := make(map[string]ToolCall)
	seenCalls := make(map[string]struct{})
	seenResults := make(map[string]struct{})

	for i, message := range messages {
		if err := message.Validate(); err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}

		results := message.ToolResults()
		if len(pending) > 0 {
			if message.Role != RoleUser {
				return fmt.Errorf("message %d: %w: unresolved tool calls before %s message", i, ErrInvalidMessage, message.Role)
			}
			for resultIndex := range results {
				result := &results[resultIndex]
				call, ok := pending[result.CallID]
				if !ok {
					return fmt.Errorf("message %d: %w: tool result for unknown call %q", i, ErrInvalidMessage, result.CallID)
				}
				if _, duplicate := seenResults[result.CallID]; duplicate {
					return fmt.Errorf("message %d: %w: duplicate result for call %q", i, ErrInvalidMessage, result.CallID)
				}
				seenResults[result.CallID] = struct{}{}
				if result.Name != "" && call.Name != "" && result.Name != call.Name {
					return fmt.Errorf("message %d: %w: result name %q does not match call name %q", i, ErrInvalidMessage, result.Name, call.Name)
				}
				if result.EffectiveKind() != call.EffectiveKind() {
					return fmt.Errorf("message %d: %w: result kind %q does not match call kind %q", i, ErrInvalidMessage, result.EffectiveKind(), call.EffectiveKind())
				}
				delete(pending, result.CallID)
			}
			if len(pending) > 0 {
				return fmt.Errorf("message %d: %w: missing results for %d tool call(s)", i, ErrInvalidMessage, len(pending))
			}
		} else if len(results) > 0 {
			return fmt.Errorf("message %d: %w: tool result without a preceding call", i, ErrInvalidMessage)
		}

		if message.Role != RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls() {
			if _, duplicate := seenCalls[call.CallID]; duplicate {
				return fmt.Errorf("message %d: %w: duplicate tool call id %q", i, ErrInvalidMessage, call.CallID)
			}
			seenCalls[call.CallID] = struct{}{}
			switch call.EffectiveKind() {
			case ToolKindFunction, ToolKindCustom, ToolKindMCP:
				pending[call.CallID] = call
			}
		}
	}
	return nil
}

// TextFromParts joins non-empty text parts with newlines.
func TextFromParts(parts []Part) string {
	var out strings.Builder
	for _, part := range parts {
		text, ok := part.(*TextPart)
		if !ok || text.Text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(text.Text)
	}
	return out.String()
}

// NonEmptyParts returns a copy with empty text parts removed.
func NonEmptyParts(parts []Part) []Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]Part, 0, len(parts))
	for _, part := range parts {
		if text, ok := part.(*TextPart); ok && text.Text == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Clone returns a deep copy of the message.
func (m Message) Clone() Message {
	clone := m
	clone.Content = cloneParts(m.Content)
	clone.Provider = m.Provider.Clone()
	return clone
}

// CloneMessages deep-copies messages. Empty and nil slices return nil.
func CloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i := range messages {
		out[i] = messages[i].Clone()
	}
	return out
}

// AppendPart returns a copy with parts appended.
func (m Message) AppendPart(parts ...Part) Message {
	m.Content = append(slices.Clone(m.Content), parts...)
	return m
}
