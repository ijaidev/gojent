package session

import (
	"context"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

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
	if err := session.Validate(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	messages := m.sessions[keyOf(session)]
	return llm.CloneMessages(messages), nil
}

// Append atomically adds a deep copy of the messages.
func (m *Memory) Append(ctx context.Context, session Session, messages ...llm.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return ErrMessagesRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	key := keyOf(session)
	m.sessions[key] = append(m.sessions[key], llm.CloneMessages(messages)...)
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
