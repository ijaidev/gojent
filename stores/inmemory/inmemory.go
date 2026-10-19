// Package inmemory provides a thread-safe, in-memory implementation of the Gojent storage interfaces.
// It is ideal for local development, unit testing, and ephemeral execution environments.
package inmemory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/storage"
)

// Store implements the storage.Storage interface in-memory.
// It holds maps for conversation threads, messages, and arbitrary thread state.
// All operations are safe for concurrent use across goroutines.
type Store struct {
	mu           sync.RWMutex
	closed       bool
	threads      map[string]*storage.Thread
	messages     map[string]*storage.Message
	threadMsgs   map[string][]string
	threadStates map[string]map[string]*storage.ThreadState
}

// New creates and initializes a new in-memory Store.
func New() *Store {
	return &Store{
		threads:      make(map[string]*storage.Thread),
		messages:     make(map[string]*storage.Message),
		threadMsgs:   make(map[string][]string),
		threadStates: make(map[string]map[string]*storage.ThreadState),
	}
}

func genID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// SaveThread creates or updates a conversation thread in memory.
// If thread.ID is empty, a unique ID is automatically generated.
func (s *Store) SaveThread(ctx context.Context, thread *storage.Thread) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if thread == nil {
		return nil, fmt.Errorf("%w: thread cannot be nil", storage.ErrInvalidInput)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	th := thread.Clone()
	if th.ID == "" {
		th.ID = genID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		if existing, ok := s.threads[th.ID]; ok && !existing.CreatedAt.IsZero() {
			th.CreatedAt = existing.CreatedAt
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now

	s.threads[th.ID] = th
	return th.Clone(), nil
}

// GetThreadByID retrieves a conversation thread by its ID.
func (s *Store) GetThreadByID(ctx context.Context, id string) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	th, ok := s.threads[id]
	if !ok {
		return nil, storage.ErrThreadNotFound
	}
	return th.Clone(), nil
}

// UpdateThread updates specific attributes of an existing thread.
func (s *Store) UpdateThread(ctx context.Context, id string, input storage.UpdateThreadInput) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	th, ok := s.threads[id]
	if !ok {
		return nil, storage.ErrThreadNotFound
	}

	if input.Title != nil {
		th.Title = *input.Title
	}
	if input.ResourceID != nil {
		th.ResourceID = *input.ResourceID
	}
	if input.Metadata != nil {
		if th.Metadata == nil {
			th.Metadata = make(map[string]any, len(input.Metadata))
		}
		maps.Copy(th.Metadata, input.Metadata)
	}
	th.UpdatedAt = time.Now().UTC()

	return th.Clone(), nil
}

// DeleteThread removes a conversation thread and cascades deletion to all associated messages and state.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return storage.ErrThreadRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return storage.ErrClosed
	}

	if _, ok := s.threads[id]; !ok {
		return storage.ErrThreadNotFound
	}

	delete(s.threads, id)
	if msgIDs, ok := s.threadMsgs[id]; ok {
		for _, msgID := range msgIDs {
			delete(s.messages, msgID)
		}
		delete(s.threadMsgs, id)
	}
	delete(s.threadStates, id)

	return nil
}

// ListThreads retrieves conversation threads matching the filter criteria.
func (s *Store) ListThreads(ctx context.Context, filter storage.ThreadFilter) ([]*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	var results []*storage.Thread
	for _, th := range s.threads {
		if filter.ResourceID != "" && th.ResourceID != filter.ResourceID {
			continue
		}
		if len(filter.Metadata) > 0 {
			match := true
			for k, v := range filter.Metadata {
				if th.Metadata == nil || !reflect.DeepEqual(th.Metadata[k], v) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		results = append(results, th.Clone())
	}

	// Sort results with tie-breaker by ID for deterministic pagination
	sortAsc := filter.SortOrder != storage.SortDesc
	slices.SortFunc(results, func(a, b *storage.Thread) int {
		var cmp int
		if filter.SortBy == storage.ThreadSortUpdatedAt {
			cmp = a.UpdatedAt.Compare(b.UpdatedAt)
		} else {
			cmp = a.CreatedAt.Compare(b.CreatedAt)
		}
		if cmp == 0 {
			cmp = strings.Compare(a.ID, b.ID)
		}
		if !sortAsc {
			cmp = -cmp
		}
		return cmp
	})

	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*storage.Thread{}, nil
		}
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// SaveMessages persists one or more messages to the in-memory store.
func (s *Store) SaveMessages(ctx context.Context, messages []*storage.Message) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, storage.ErrClosed
	}
	if len(messages) == 0 {
		return []*storage.Message{}, nil
	}

	out := make([]*storage.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			return nil, fmt.Errorf("%w: message cannot be nil", storage.ErrInvalidInput)
		}
		if msg.ThreadID == "" {
			return nil, fmt.Errorf("%w: message requires thread_id", storage.ErrThreadRequired)
		}

		mc := msg.Clone()
		if mc.ID == "" {
			mc.ID = genID("msg")
		}
		now := time.Now().UTC()
		if oldMsg, exists := s.messages[mc.ID]; exists {
			if mc.CreatedAt.IsZero() {
				mc.CreatedAt = oldMsg.CreatedAt
			}
			if oldMsg.ThreadID != mc.ThreadID {
				if list, ok := s.threadMsgs[oldMsg.ThreadID]; ok {
					s.threadMsgs[oldMsg.ThreadID] = slices.DeleteFunc(list, func(id string) bool {
						return id == mc.ID
					})
				}
			}
		} else if mc.CreatedAt.IsZero() {
			mc.CreatedAt = now
		}

		if mc.ResourceID == "" {
			if parent, ok := s.threads[mc.ThreadID]; ok {
				mc.ResourceID = parent.ResourceID
			}
		}

		s.messages[mc.ID] = mc
		if !slices.Contains(s.threadMsgs[mc.ThreadID], mc.ID) {
			s.threadMsgs[mc.ThreadID] = append(s.threadMsgs[mc.ThreadID], mc.ID)
		}

		if th, ok := s.threads[mc.ThreadID]; ok {
			th.UpdatedAt = mc.CreatedAt
		}

		out = append(out, mc.Clone())
	}

	return out, nil
}

// ListMessages retrieves messages matching the filter criteria.
func (s *Store) ListMessages(ctx context.Context, filter storage.MessageFilter) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	var candidates []*storage.Message
	if filter.ThreadID != "" {
		for _, msgID := range s.threadMsgs[filter.ThreadID] {
			if msg, ok := s.messages[msgID]; ok {
				candidates = append(candidates, msg)
			}
		}
	} else {
		for _, msg := range s.messages {
			candidates = append(candidates, msg)
		}
	}

	var results []*storage.Message
	for _, msg := range candidates {
		if filter.ResourceID != "" && msg.ResourceID != filter.ResourceID {
			continue
		}
		if filter.Role != "" && msg.Role != filter.Role {
			continue
		}
		if filter.Since != nil && msg.CreatedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && msg.CreatedAt.After(*filter.Until) {
			continue
		}
		results = append(results, msg.Clone())
	}

	// Sort stably to preserve insertion order for identical timestamps
	sortAsc := filter.SortOrder != storage.SortDesc
	slices.SortStableFunc(results, func(a, b *storage.Message) int {
		cmp := a.CreatedAt.Compare(b.CreatedAt)
		if !sortAsc {
			cmp = -cmp
		}
		return cmp
	})

	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*storage.Message{}, nil
		}
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// GetMessagesByID retrieves messages matching the provided IDs.
func (s *Store) GetMessagesByID(ctx context.Context, ids []string) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	var results []*storage.Message
	for _, id := range ids {
		if msg, ok := s.messages[id]; ok {
			results = append(results, msg.Clone())
		}
	}
	return results, nil
}

// DeleteMessages removes messages matching the provided IDs.
func (s *Store) DeleteMessages(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return storage.ErrClosed
	}

	for _, id := range ids {
		if msg, ok := s.messages[id]; ok {
			delete(s.messages, id)
			if list, exists := s.threadMsgs[msg.ThreadID]; exists {
				s.threadMsgs[msg.ThreadID] = slices.DeleteFunc(list, func(item string) bool {
					return item == id
				})
			}
		}
	}
	return nil
}

// GetThreadState retrieves the durable state value for a (threadID, stateType) pair.
func (s *Store) GetThreadState(ctx context.Context, threadID, stateType string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if threadID == "" || stateType == "" {
		return nil, fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	byType, ok := s.threadStates[threadID]
	if !ok {
		return nil, storage.ErrStateNotFound
	}
	record, ok := byType[stateType]
	if !ok {
		return nil, storage.ErrStateNotFound
	}
	return record.Value, nil
}

// SetThreadState sets or overwrites the durable state value for a (threadID, stateType) pair.
func (s *Store) SetThreadState(ctx context.Context, threadID, stateType string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return storage.ErrClosed
	}

	byType, ok := s.threadStates[threadID]
	if !ok {
		byType = make(map[string]*storage.ThreadState)
		s.threadStates[threadID] = byType
	}
	byType[stateType] = &storage.ThreadState{
		ThreadID:  threadID,
		Type:      stateType,
		Value:     value,
		UpdatedAt: time.Now().UTC(),
	}
	return nil
}

// DeleteThreadState removes the durable state value for a (threadID, stateType) pair.
func (s *Store) DeleteThreadState(ctx context.Context, threadID, stateType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return storage.ErrClosed
	}

	if byType, ok := s.threadStates[threadID]; ok {
		delete(byType, stateType)
		if len(byType) == 0 {
			delete(s.threadStates, threadID)
		}
	}
	return nil
}

// ListThreadStates returns all state values recorded for a thread, mapped by state type.
func (s *Store) ListThreadStates(ctx context.Context, threadID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if threadID == "" {
		return nil, fmt.Errorf("%w: threadID cannot be empty", storage.ErrInvalidInput)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, storage.ErrClosed
	}

	byType, ok := s.threadStates[threadID]
	if !ok {
		return make(map[string]any), nil
	}
	res := make(map[string]any, len(byType))
	for k, v := range byType {
		res[k] = v.Value
	}
	return res, nil
}

// Clear removes all stored threads, messages, and state. Useful in testing.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads = make(map[string]*storage.Thread)
	s.messages = make(map[string]*storage.Message)
	s.threadMsgs = make(map[string][]string)
	s.threadStates = make(map[string]map[string]*storage.ThreadState)
}

// DangerouslyClearAll removes all stored threads, messages, and state.
func (s *Store) DangerouslyClearAll() {
	s.Clear()
}

// Close marks the store as closed. Subsequent operations will return storage.ErrClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Ensure Store satisfies Storage, MemoryStore, and all domain store interfaces at compile time.
var (
	_ storage.Storage          = (*Store)(nil)
	_ storage.MemoryStore      = (*Store)(nil)
	_ storage.ThreadStore      = (*Store)(nil)
	_ storage.MessageStore     = (*Store)(nil)
	_ storage.ThreadStateStore = (*Store)(nil)
)
