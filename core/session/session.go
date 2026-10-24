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

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrIDRequired is returned when a store operation receives a session
	// without an ID.
	ErrIDRequired = errors.New("session: id is required")
	// ErrMessagesRequired is returned when Append receives no messages.
	ErrMessagesRequired = errors.New("session: at least one message is required")
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

// Validate checks the session before a store operation.
func (s Session) Validate() error {
	if s.ID == "" {
		return ErrIDRequired
	}
	return nil
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

var _ Store = (*Memory)(nil)
