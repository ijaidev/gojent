package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// CompositeStore routes storage domain operations (threads, messages, thread state)
// to dedicated backends while falling back to a default store for unconfigured domains.
type CompositeStore struct {
	mu           sync.RWMutex
	closed       bool
	defaultStore Storage
	threadStore  ThreadStore
	messageStore MessageStore
	stateStore   ThreadStateStore
}

// CompositeOption configures a CompositeStore.
type CompositeOption func(*CompositeStore)

// WithDefaultStore sets the fallback storage backend for any domain not explicitly overridden.
func WithDefaultStore(s Storage) CompositeOption {
	return func(c *CompositeStore) {
		c.defaultStore = s
	}
}

// WithThreadStore routes thread operations to a dedicated ThreadStore.
func WithThreadStore(ts ThreadStore) CompositeOption {
	return func(c *CompositeStore) {
		c.threadStore = ts
	}
}

// WithMessageStore routes message operations to a dedicated MessageStore.
func WithMessageStore(ms MessageStore) CompositeOption {
	return func(c *CompositeStore) {
		c.messageStore = ms
	}
}

// WithThreadStateStore routes thread state operations to a dedicated ThreadStateStore.
func WithThreadStateStore(tss ThreadStateStore) CompositeOption {
	return func(c *CompositeStore) {
		c.stateStore = tss
	}
}

// NewComposite creates a new CompositeStore with the provided options.
// At least one store (either a default store or all three domain stores) must be configured.
func NewComposite(opts ...CompositeOption) (*CompositeStore, error) {
	c := &CompositeStore{}
	for _, opt := range opts {
		opt(c)
	}

	if c.defaultStore == nil && (c.threadStore == nil || c.messageStore == nil || c.stateStore == nil) {
		return nil, fmt.Errorf("%w: composite store requires either a default store or all domain stores configured", ErrInvalidInput)
	}

	return c, nil
}

func (c *CompositeStore) checkClosed() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return ErrClosed
	}
	return nil
}

func (c *CompositeStore) getThreadStore() (ThreadStore, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.threadStore != nil {
		return c.threadStore, nil
	}
	if c.defaultStore != nil {
		return c.defaultStore, nil
	}
	return nil, fmt.Errorf("%w: no thread store configured", ErrInvalidInput)
}

func (c *CompositeStore) getMessageStore() (MessageStore, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.messageStore != nil {
		return c.messageStore, nil
	}
	if c.defaultStore != nil {
		return c.defaultStore, nil
	}
	return nil, fmt.Errorf("%w: no message store configured", ErrInvalidInput)
}

func (c *CompositeStore) getStateStore() (ThreadStateStore, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.stateStore != nil {
		return c.stateStore, nil
	}
	if c.defaultStore != nil {
		return c.defaultStore, nil
	}
	return nil, fmt.Errorf("%w: no thread state store configured", ErrInvalidInput)
}

// SaveThread routes thread saving to the designated ThreadStore.
func (c *CompositeStore) SaveThread(ctx context.Context, thread *Thread) (*Thread, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ts, err := c.getThreadStore()
	if err != nil {
		return nil, err
	}
	return ts.SaveThread(ctx, thread)
}

// GetThreadByID routes thread retrieval to the designated ThreadStore.
func (c *CompositeStore) GetThreadByID(ctx context.Context, id string) (*Thread, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ts, err := c.getThreadStore()
	if err != nil {
		return nil, err
	}
	return ts.GetThreadByID(ctx, id)
}

// UpdateThread routes thread update to the designated ThreadStore.
func (c *CompositeStore) UpdateThread(ctx context.Context, id string, input UpdateThreadInput) (*Thread, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ts, err := c.getThreadStore()
	if err != nil {
		return nil, err
	}
	return ts.UpdateThread(ctx, id, input)
}

// DeleteThread routes thread deletion to the designated ThreadStore and cascades
// to separate message and state stores if different backends are wired.
func (c *CompositeStore) DeleteThread(ctx context.Context, id string) error {
	if err := c.checkClosed(); err != nil {
		return err
	}
	ts, err := c.getThreadStore()
	if err != nil {
		return err
	}

	if err := ts.DeleteThread(ctx, id); err != nil {
		return err
	}

	// If the message store is a distinct backend, cascade message cleanup
	ms, err := c.getMessageStore()
	if err == nil && any(ms) != any(ts) {
		msgs, listErr := ms.ListMessages(ctx, MessageFilter{ThreadID: id})
		if listErr == nil && len(msgs) > 0 {
			ids := make([]string, len(msgs))
			for i, m := range msgs {
				ids[i] = m.ID
			}
			_ = ms.DeleteMessages(ctx, ids)
		}
	}

	// If the state store is a distinct backend, cascade state cleanup
	ss, err := c.getStateStore()
	if err == nil && any(ss) != any(ts) {
		states, listErr := ss.ListThreadStates(ctx, id)
		if listErr == nil {
			for stateType := range states {
				_ = ss.DeleteThreadState(ctx, id, stateType)
			}
		}
	}

	return nil
}

// ListThreads routes thread listing to the designated ThreadStore.
func (c *CompositeStore) ListThreads(ctx context.Context, filter ThreadFilter) ([]*Thread, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ts, err := c.getThreadStore()
	if err != nil {
		return nil, err
	}
	return ts.ListThreads(ctx, filter)
}

// SaveMessages routes message saving to the designated MessageStore.
func (c *CompositeStore) SaveMessages(ctx context.Context, messages []*Message) ([]*Message, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ms, err := c.getMessageStore()
	if err != nil {
		return nil, err
	}
	return ms.SaveMessages(ctx, messages)
}

// ListMessages routes message querying to the designated MessageStore.
func (c *CompositeStore) ListMessages(ctx context.Context, filter MessageFilter) ([]*Message, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ms, err := c.getMessageStore()
	if err != nil {
		return nil, err
	}
	return ms.ListMessages(ctx, filter)
}

// GetMessagesByID routes batch message retrieval to the designated MessageStore.
func (c *CompositeStore) GetMessagesByID(ctx context.Context, ids []string) ([]*Message, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ms, err := c.getMessageStore()
	if err != nil {
		return nil, err
	}
	return ms.GetMessagesByID(ctx, ids)
}

// DeleteMessages routes message deletion to the designated MessageStore.
func (c *CompositeStore) DeleteMessages(ctx context.Context, ids []string) error {
	if err := c.checkClosed(); err != nil {
		return err
	}
	ms, err := c.getMessageStore()
	if err != nil {
		return err
	}
	return ms.DeleteMessages(ctx, ids)
}

// GetThreadState routes state retrieval to the designated ThreadStateStore.
func (c *CompositeStore) GetThreadState(ctx context.Context, threadID string, stateType string) (any, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ss, err := c.getStateStore()
	if err != nil {
		return nil, err
	}
	return ss.GetThreadState(ctx, threadID, stateType)
}

// SetThreadState routes state setting to the designated ThreadStateStore.
func (c *CompositeStore) SetThreadState(ctx context.Context, threadID string, stateType string, value any) error {
	if err := c.checkClosed(); err != nil {
		return err
	}
	ss, err := c.getStateStore()
	if err != nil {
		return err
	}
	return ss.SetThreadState(ctx, threadID, stateType, value)
}

// DeleteThreadState routes state deletion to the designated ThreadStateStore.
func (c *CompositeStore) DeleteThreadState(ctx context.Context, threadID string, stateType string) error {
	if err := c.checkClosed(); err != nil {
		return err
	}
	ss, err := c.getStateStore()
	if err != nil {
		return err
	}
	return ss.DeleteThreadState(ctx, threadID, stateType)
}

// ListThreadStates routes state listing to the designated ThreadStateStore.
func (c *CompositeStore) ListThreadStates(ctx context.Context, threadID string) (map[string]any, error) {
	if err := c.checkClosed(); err != nil {
		return nil, err
	}
	ss, err := c.getStateStore()
	if err != nil {
		return nil, err
	}
	return ss.ListThreadStates(ctx, threadID)
}

// Close closes all unique storage backends managed by the CompositeStore.
func (c *CompositeStore) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	c.closed = true

	type closer interface {
		Close() error
	}

	var closeErrs []error
	closedPtrs := make(map[uintptr]struct{})

	checkAndClose := func(s any) {
		if s == nil {
			return
		}
		val := reflect.ValueOf(s)
		if val.Kind() == reflect.Pointer {
			ptr := val.Pointer()
			if _, ok := closedPtrs[ptr]; ok {
				return
			}
			closedPtrs[ptr] = struct{}{}
		}
		if cl, ok := s.(closer); ok {
			if err := cl.Close(); err != nil {
				closeErrs = append(closeErrs, err)
			}
		}
	}

	checkAndClose(c.defaultStore)
	checkAndClose(c.threadStore)
	checkAndClose(c.messageStore)
	checkAndClose(c.stateStore)

	return errors.Join(closeErrs...)
}

// Ensure CompositeStore satisfies Storage and MemoryStore at compile time.
var (
	_ Storage     = (*CompositeStore)(nil)
	_ MemoryStore = (*CompositeStore)(nil)
)
