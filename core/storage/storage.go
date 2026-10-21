// Package storage defines the persistence models, domain contracts, and interfaces
// for managing conversation threads and messages in Gojent.
package storage

import "context"

// ThreadStore defines persistence operations for conversation threads.
type ThreadStore interface {
	// SaveThread creates or updates a conversation thread.
	// If thread.ID is empty, a unique ID is generated and returned on the persisted thread.
	SaveThread(ctx context.Context, thread *Thread) (*Thread, error)

	// GetThreadByID retrieves a conversation thread by its unique identifier.
	// Returns ErrThreadNotFound if no thread matches the given ID.
	GetThreadByID(ctx context.Context, id string) (*Thread, error)

	// UpdateThread updates specific attributes (e.g. title, metadata) of an existing thread.
	// Returns ErrThreadNotFound if the thread does not exist.
	UpdateThread(ctx context.Context, id string, input UpdateThreadInput) (*Thread, error)

	// DeleteThread removes a conversation thread and cascades deletion to its messages.
	// Returns ErrThreadNotFound if the thread does not exist.
	DeleteThread(ctx context.Context, id string) error

	// ListThreads retrieves a list of conversation threads matching the specified filter.
	ListThreads(ctx context.Context, filter ThreadFilter) ([]*Thread, error)
}

// MessageStore defines persistence operations for conversation messages.
type MessageStore interface {
	// SaveMessages persists one or more messages to their respective threads.
	// Any message with an empty ID is assigned a unique ID before persistence.
	SaveMessages(ctx context.Context, messages []*Message) ([]*Message, error)

	// ListMessages retrieves messages matching the specified filter (typically scoped by thread ID).
	ListMessages(ctx context.Context, filter MessageFilter) ([]*Message, error)

	// GetMessagesByID retrieves messages matching the given list of message IDs.
	GetMessagesByID(ctx context.Context, ids []string) ([]*Message, error)

	// DeleteMessages removes messages matching the given list of message IDs.
	DeleteMessages(ctx context.Context, ids []string) error
}

// Storage represents the complete swappable storage backend in Gojent.
// It unites the thread and message stores with lifecycle management.
type Storage interface {
	ThreadStore
	MessageStore
	Close() error
}
