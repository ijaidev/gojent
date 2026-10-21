package app

import "context"

// Store is the transcript persistence seam. Implementations must be safe for
// concurrent use by multiple runs and HTTP handlers.
type Store interface {
	CreateThread(ctx context.Context, thread *Thread) error
	GetThread(ctx context.Context, id string) (*Thread, error)
	ListThreads(ctx context.Context) ([]*Thread, error)
	UpdateThread(ctx context.Context, thread *Thread) error
	DeleteThread(ctx context.Context, id string) error

	AppendMessages(ctx context.Context, messages ...*Message) error
	ListMessages(ctx context.Context, threadID string) ([]*Message, error)

	Close() error
}
