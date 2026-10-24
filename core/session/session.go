// Package session defines conversation identity and the minimal persistence
// contract used by the agent runner.
//
// A Store has exactly two operations: Load and the atomic Append. Advanced
// durability features such as sequences, leases, and checkpoints belong in
// store modules, not in the core.
package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrIDRequired is returned when a store operation receives a session
	// without an ID.
	ErrIDRequired = errors.New("session: id is required")
	// ErrNilStoreMessage is returned when Append receives no messages.
	ErrNilStoreMessage = errors.New("session: no messages to append")
	// ErrClosed is returned when the in-memory store is used after Close.
	ErrClosed = errors.New("session: store is closed")
)

// Session identifies a conversation and its owner scope.
type Session struct {
	// ID identifies the conversation.
	ID string
	// ResourceID identifies the owner or tenant. Stores must use it to isolate
	// conversations that happen to share an ID.
	ResourceID string
}

// Store persists conversation messages.
//
// Append must be atomic: either every message is persisted or none is. The
// runner calls Append once per step with the assistant message and all tool
// results, so a crash cannot leave a transcript with unanswered tool calls.
type Store interface {
	// Load returns the session's messages in chronological order.
	Load(ctx context.Context, session Session) ([]llm.Message, error)
	// Append atomically adds messages to the session.
	Append(ctx context.Context, session Session, messages ...llm.Message) error
}

// Memory is a thread-safe in-memory Store. It is intended for tests, examples,
// and ephemeral applications.
type Memory struct {
	mu       sync.RWMutex
	closed   bool
	sessions map[sessionKey][]llm.Message
}

type sessionKey struct {
	id         string
	resourceID string
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{sessions: make(map[sessionKey][]llm.Message)}
}

// Load returns a deep copy of the session's messages.
func (m *Memory) Load(ctx context.Context, session Session) ([]llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session.ID == "" {
		return nil, ErrIDRequired
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	messages := m.sessions[keyOf(session)]
	return cloneMessages(messages), nil
}

// Append atomically adds a deep copy of the messages.
func (m *Memory) Append(ctx context.Context, session Session, messages ...llm.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session.ID == "" {
		return ErrIDRequired
	}
	if len(messages) == 0 {
		return ErrNilStoreMessage
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	key := keyOf(session)
	m.sessions[key] = append(m.sessions[key], cloneMessages(messages)...)
	return nil
}

// Close makes subsequent operations fail with ErrClosed.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Clear removes all sessions. It is intended for tests.
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = make(map[sessionKey][]llm.Message)
}

func keyOf(session Session) sessionKey {
	return sessionKey{id: session.ID, resourceID: session.ResourceID}
}

func cloneMessages(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]llm.Message, len(messages))
	for i := range messages {
		out[i] = messages[i].Clone()
	}
	return out
}

// Validate checks the session before a store operation.
func (s Session) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("%w", ErrIDRequired)
	}
	return nil
}

// Messages is a convenience wrapper that snapshots a message slice before
// exposing it. It exists to keep adapters from accidentally aliasing store
// state.
func Messages(messages []llm.Message) []llm.Message {
	return slices.Clone(messages)
}

var _ Store = (*Memory)(nil)
