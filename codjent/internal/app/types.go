package app

import (
	"crypto/rand"
	"encoding/hex"
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

// Message is one persisted conversation message.
type Message struct {
	ID         string         `json:"id"`
	ThreadID   string         `json:"thread_id"`
	Role       llm.Role       `json:"role"`
	Content    []llm.Part     `json:"content"`
	Thinking   string         `json:"thinking,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Text returns the textual content of a message.
func (m *Message) Text() string {
	return llm.TextFromParts(m.Content)
}

// ToLLM converts a persisted message to gojent's message type. Empty text
// parts are omitted because some providers reject empty text content.
func (m *Message) ToLLM() llm.Message {
	content := make([]llm.Part, 0, len(m.Content))
	for _, part := range m.Content {
		if part.Type == llm.PartTypeText && part.Text == "" {
			continue
		}
		content = append(content, part)
	}
	return llm.Message{
		Role:       m.Role,
		Content:    content,
		Thinking:   m.Thinking,
		Name:       m.Name,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	}
}

// CreateThreadInput is the request body for creating a thread.
type CreateThreadInput struct {
	Title      string     `json:"title"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	AccessMode AccessMode `json:"access_mode"`
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
	Thread   *Thread    `json:"thread"`
	Messages []*Message `json:"messages"`
}

func messageFromLLM(threadID string, m *llm.Message, now time.Time) *Message {
	content := make([]llm.Part, 0, len(m.Content))
	for _, part := range m.Content {
		if part.Type == llm.PartTypeText && part.Text == "" {
			continue
		}
		content = append(content, part)
	}
	if m.Role == llm.RoleAssistant && len(content) == 0 && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Thinking) == "" {
		return nil
	}
	return &Message{
		ID:         newID("msg"),
		ThreadID:   threadID,
		Role:       m.Role,
		Content:    content,
		Thinking:   m.Thinking,
		Name:       m.Name,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		CreatedAt:  now,
	}
}

func userMessage(threadID, content string, now time.Time) *Message {
	return &Message{
		ID:        newID("msg"),
		ThreadID:  threadID,
		Role:      llm.RoleUser,
		Content:   []llm.Part{llm.TextPart(content)},
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
