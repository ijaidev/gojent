package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrThreadNotFound is returned when a thread does not exist.
	ErrThreadNotFound = errors.New("thread not found")
	// ErrRunNotFound is returned when a run does not exist.
	ErrRunNotFound = errors.New("run not found")
	// ErrApprovalNotFound is returned when a run has no matching pending approval.
	ErrApprovalNotFound = errors.New("approval not found")
	// ErrThreadBusy is returned when a thread already has an active run.
	ErrThreadBusy = errors.New("thread already has an active run")
	// ErrRunFinished is returned when an operation needs an active run.
	ErrRunFinished = errors.New("run is already finished")
	// ErrClosed is returned after the service has begun shutting down.
	ErrClosed = errors.New("service is closed")
	// ErrInvalidInput is returned for invalid request data.
	ErrInvalidInput = errors.New("invalid input")
)

const defaultThreadTitle = "New thread"

// AccessMode controls whether mutating tools require explicit approval.
type AccessMode string

const (
	// AccessAsk asks before running commands.
	AccessAsk AccessMode = "ask"
	// AccessFull runs tools without approval prompts.
	AccessFull AccessMode = "full"
	// DefaultAccessMode is the safe default.
	DefaultAccessMode = AccessAsk
)

// Valid reports whether the mode is recognized.
func (m AccessMode) Valid() bool {
	return m == AccessAsk || m == AccessFull
}

// Thread is a persisted conversation and its model settings.
type Thread struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	AccessMode AccessMode `json:"access_mode"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Message is one persisted conversation message: the canonical provider-neutral
// payload plus the app envelope (identity, thread, timestamp).
//
// There is no stored Thinking, ToolCalls, or ToolCallID. Those are read-only
// views derived from canonical parts by MessageView.
type Message struct {
	ID        string      `json:"id"`
	ThreadID  string      `json:"thread_id"`
	Payload   llm.Message `json:"-"`
	CreatedAt time.Time   `json:"created_at"`
}

// Role returns the canonical message role.
func (m *Message) Role() llm.Role {
	if m == nil {
		return ""
	}
	return m.Payload.Role
}

// Text returns the textual content of a message.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	return m.Payload.Text()
}

// ToolCalls returns the canonical tool calls in content order.
func (m *Message) ToolCalls() []llm.ToolCall {
	if m == nil {
		return nil
	}
	return m.Payload.ToolCalls()
}

// ToolResults returns the canonical tool results in content order.
func (m *Message) ToolResults() []llm.ToolResult {
	if m == nil {
		return nil
	}
	return m.Payload.ToolResults()
}

// Thinking returns the readable reasoning content in part order.
func (m *Message) Thinking() string {
	if m == nil {
		return ""
	}
	return reasoningText(m.Payload.Content)
}

// ToLLM returns a deep copy of the canonical payload with empty text parts
// removed. Some providers reject empty text content.
func (m *Message) ToLLM() llm.Message {
	if m == nil {
		return llm.Message{}
	}
	payload := m.Payload.Clone()
	payload.Content = llm.NonEmptyParts(payload.Content)
	return payload
}

// MarshalJSON encodes the message as one display view. A message that expands
// into multiple views (grouped tool results) must be serialized through
// ThreadDetail or MessageViews.
func (m Message) MarshalJSON() ([]byte, error) {
	views := m.Views()
	if len(views) != 1 {
		return nil, fmt.Errorf("app: message %q expands to %d views; use MessageViews", m.ID, len(views))
	}
	return json.Marshal(views[0])
}

// PartView is the frontend-facing projection of a displayable canonical part.
// Provider replay metadata is never exposed.
type PartView struct {
	Type     llm.PartType `json:"type"`
	Text     string       `json:"text,omitempty"`
	MIMEType string       `json:"mime_type,omitempty"`
	Data     []byte       `json:"data,omitempty"`
	URL      string       `json:"url,omitempty"`
	Filename string       `json:"filename,omitempty"`
}

// ToolCallView is the frontend-facing projection of a canonical tool call.
type ToolCallView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// MessageView is the stable JSON contract served to the frontend. Thinking and
// tool calls are derived from canonical parts; tool results are expanded into
// one "tool" view per canonical result so the existing pairing logic works.
type MessageView struct {
	ID         string         `json:"id"`
	ThreadID   string         `json:"thread_id"`
	Role       string         `json:"role"`
	Content    []PartView     `json:"content"`
	Thinking   string         `json:"thinking,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCallView `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Views projects a canonical message onto the frontend contract. Tool results
// become one "tool" message each, in content order.
func (m *Message) Views() []MessageView {
	if m == nil {
		return nil
	}
	switch m.Payload.Role {
	case llm.RoleAssistant:
		return []MessageView{{
			ID:        m.ID,
			ThreadID:  m.ThreadID,
			Role:      string(llm.RoleAssistant),
			Content:   partViews(m.Payload.Content),
			Thinking:  reasoningText(m.Payload.Content),
			Name:      m.Payload.Name,
			ToolCalls: toolCallViews(m.Payload.Content),
			CreatedAt: m.CreatedAt,
		}}
	case llm.RoleSystem, llm.RoleDeveloper:
		return []MessageView{{
			ID:        m.ID,
			ThreadID:  m.ThreadID,
			Role:      string(llm.RoleSystem),
			Content:   partViews(m.Payload.Content),
			Name:      m.Payload.Name,
			CreatedAt: m.CreatedAt,
		}}
	case llm.RoleUser:
		return m.userViews()
	default:
		return nil
	}
}

func (m *Message) userViews() []MessageView {
	var views []MessageView
	var pending []llm.Part
	textSegments := 0
	flush := func() {
		if len(pending) == 0 {
			return
		}
		id := m.ID
		if textSegments > 0 {
			// A user message can interleave text and tool results. Keep the
			// canonical ID for the first text view and derive unique IDs for
			// any later segment.
			id = fmt.Sprintf("%s:text:%d", m.ID, textSegments)
		}
		textSegments++
		views = append(views, MessageView{
			ID:        id,
			ThreadID:  m.ThreadID,
			Role:      string(llm.RoleUser),
			Content:   partViews(pending),
			Name:      m.Payload.Name,
			CreatedAt: m.CreatedAt,
		})
		pending = nil
	}

	for _, part := range m.Payload.Content {
		if result, ok := part.(*llm.ToolResultPart); ok {
			flush()
			views = append(views, MessageView{
				ID:         fmt.Sprintf("%s:result:%d", m.ID, len(views)),
				ThreadID:   m.ThreadID,
				Role:       "tool",
				Content:    partViews(result.Content),
				Name:       result.Name,
				ToolCallID: result.CallID,
				CreatedAt:  m.CreatedAt,
			})
			continue
		}
		if displayable(part) {
			pending = append(pending, part)
		}
	}
	flush()
	return views
}

// MessageViews projects every message in transcript order.
func MessageViews(messages []*Message) []MessageView {
	views := make([]MessageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, message.Views()...)
	}
	return views
}

func partViews(parts []llm.Part) []PartView {
	views := make([]PartView, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case *llm.TextPart:
			views = append(views, PartView{Type: llm.PartTypeText, Text: p.Text})
		case *llm.RefusalPart:
			views = append(views, PartView{Type: llm.PartTypeRefusal, Text: p.Text})
		case *llm.FilePart:
			views = append(views, PartView{
				Type:     llm.PartTypeFile,
				MIMEType: p.MIMEType,
				Data:     p.Data,
				URL:      p.URL,
				Filename: p.Filename,
			})
		}
	}
	return views
}

func displayable(part llm.Part) bool {
	switch part.(type) {
	case *llm.TextPart, *llm.RefusalPart, *llm.FilePart:
		return true
	default:
		return false
	}
}

func toolCallViews(parts []llm.Part) []ToolCallView {
	var views []ToolCallView
	for _, part := range parts {
		call, ok := part.(*llm.ToolCallPart)
		if !ok || call.EffectiveKind() == llm.ToolKindServer {
			continue
		}
		arguments := string(call.Input)
		if arguments == "" {
			arguments = "{}"
		}
		views = append(views, ToolCallView{
			ID:        call.CallID,
			Name:      call.Name,
			Arguments: arguments,
		})
	}
	return views
}

func reasoningText(parts []llm.Part) string {
	var segments []string
	for _, part := range parts {
		reasoning, ok := part.(*llm.ReasoningPart)
		if !ok {
			continue
		}
		if reasoning.Text != "" {
			segments = append(segments, reasoning.Text)
		}
		for _, summary := range reasoning.Summary {
			if summary != "" {
				segments = append(segments, summary)
			}
		}
	}
	return strings.Join(segments, "\n")
}

// CreateThreadInput is the request body for creating a thread.
type CreateThreadInput struct {
	Title      string     `json:"title"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	AccessMode AccessMode `json:"access_mode"`
}

// StartMessageInput is the request body for starting a user message. Content
// may be empty when at least one attachment is present.
type StartMessageInput struct {
	Content     string            `json:"content"`
	Attachments []AttachmentInput `json:"attachments,omitempty"`
}

// UpdateThreadInput is a partial thread update.
type UpdateThreadInput struct {
	Title      *string     `json:"title,omitempty"`
	Provider   *string     `json:"provider,omitempty"`
	Model      *string     `json:"model,omitempty"`
	AccessMode *AccessMode `json:"access_mode,omitempty"`
}

// ThreadDetail is a thread with its full transcript.
type ThreadDetail struct {
	Thread    *Thread    `json:"thread"`
	Messages  []*Message `json:"messages"`
	ActiveRun *RunInfo   `json:"active_run,omitempty"`
}

// MarshalJSON serves the stable frontend contract: canonical messages are
// projected into display views with derived thinking and tool calls.
func (d ThreadDetail) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Thread    *Thread       `json:"thread"`
		Messages  []MessageView `json:"messages"`
		ActiveRun *RunInfo      `json:"active_run,omitempty"`
	}{
		Thread:    d.Thread,
		Messages:  MessageViews(d.Messages),
		ActiveRun: d.ActiveRun,
	})
}

func messageFromLLM(threadID string, m *llm.Message, now time.Time) *Message {
	payload := m.Clone()
	payload.Content = llm.NonEmptyParts(payload.Content)
	if len(payload.Content) == 0 {
		return nil
	}
	return &Message{
		ID:        newID("msg"),
		ThreadID:  threadID,
		Payload:   payload,
		CreatedAt: now,
	}
}

func userMessageWithParts(threadID string, parts []llm.Part, now time.Time) *Message {
	return &Message{
		ID:        newID("msg"),
		ThreadID:  threadID,
		Payload:   llm.UserMessageWithParts(parts...),
		CreatedAt: now,
	}
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const maxRunes = 60
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}
