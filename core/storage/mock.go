package storage

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
)

// MockStorage is an in-memory implementation of the Storage interface,
// primarily intended for testing and demonstration in the core package.
type MockStorage struct {
	mu           sync.RWMutex
	closed       bool
	threads      map[string]*Thread
	messages     map[string]*Message
	threadMsgs   map[string][]string
	threadStates map[string]map[string]*ThreadState
}

// NewMockStorage instantiates an initialized MockStorage.
func NewMockStorage() *MockStorage {
	return &MockStorage{
		threads:      make(map[string]*Thread),
		messages:     make(map[string]*Message),
		threadMsgs:   make(map[string][]string),
		threadStates: make(map[string]map[string]*ThreadState),
	}
}

func generateID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// SaveThread saves or creates a thread.
func (m *MockStorage) SaveThread(ctx context.Context, thread *Thread) (*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if thread == nil {
		return nil, fmt.Errorf("%w: thread cannot be nil", ErrInvalidInput)
	}

	th := thread.Clone()
	if th.ID == "" {
		th.ID = generateID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		if existing, ok := m.threads[th.ID]; ok && !existing.CreatedAt.IsZero() {
			th.CreatedAt = existing.CreatedAt
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now

	m.threads[th.ID] = th
	return th.Clone(), nil
}

// GetThreadByID retrieves a thread by ID.
func (m *MockStorage) GetThreadByID(ctx context.Context, id string) (*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrThreadRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	th, ok := m.threads[id]
	if !ok {
		return nil, ErrThreadNotFound
	}
	return th.Clone(), nil
}

// UpdateThread updates specific attributes of an existing thread.
func (m *MockStorage) UpdateThread(ctx context.Context, id string, input UpdateThreadInput) (*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrThreadRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}

	th, ok := m.threads[id]
	if !ok {
		return nil, ErrThreadNotFound
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

// DeleteThread removes a thread and cascades deletion to messages and thread states.
func (m *MockStorage) DeleteThread(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrThreadRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	if _, ok := m.threads[id]; !ok {
		return ErrThreadNotFound
	}

	delete(m.threads, id)
	if msgIDs, ok := m.threadMsgs[id]; ok {
		for _, msgID := range msgIDs {
			delete(m.messages, msgID)
		}
		delete(m.threadMsgs, id)
	}
	delete(m.threadStates, id)

	return nil
}

// ListThreads retrieves threads matching the filter.
func (m *MockStorage) ListThreads(ctx context.Context, filter ThreadFilter) ([]*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	var results []*Thread
	for _, th := range m.threads {
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
	sortAsc := filter.SortOrder != SortDesc
	slices.SortFunc(results, func(a, b *Thread) int {
		var cmp int
		if filter.SortBy == ThreadSortUpdatedAt {
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

	// Pagination
	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*Thread{}, nil
		}
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// SaveMessages persists messages to storage.
func (m *MockStorage) SaveMessages(ctx context.Context, messages []*Message) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if len(messages) == 0 {
		return []*Message{}, nil
	}

	out := make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			return nil, fmt.Errorf("%w: message cannot be nil", ErrInvalidInput)
		}
		if msg.ThreadID == "" {
			return nil, fmt.Errorf("%w: message requires thread_id", ErrThreadRequired)
		}

		mc := msg.Clone()
		if mc.ID == "" {
			mc.ID = generateID("msg")
		}
		now := time.Now().UTC()
		if oldMsg, exists := m.messages[mc.ID]; exists {
			if mc.CreatedAt.IsZero() {
				mc.CreatedAt = oldMsg.CreatedAt
			}
			if oldMsg.ThreadID != mc.ThreadID {
				if list, ok := m.threadMsgs[oldMsg.ThreadID]; ok {
					m.threadMsgs[oldMsg.ThreadID] = slices.DeleteFunc(list, func(id string) bool {
						return id == mc.ID
					})
				}
			}
		} else if mc.CreatedAt.IsZero() {
			mc.CreatedAt = now
		}

		if mc.ResourceID == "" {
			if parent, ok := m.threads[mc.ThreadID]; ok {
				mc.ResourceID = parent.ResourceID
			}
		}

		m.messages[mc.ID] = mc
		if !slices.Contains(m.threadMsgs[mc.ThreadID], mc.ID) {
			m.threadMsgs[mc.ThreadID] = append(m.threadMsgs[mc.ThreadID], mc.ID)
		}

		if th, ok := m.threads[mc.ThreadID]; ok {
			th.UpdatedAt = mc.CreatedAt
		}

		out = append(out, mc.Clone())
	}

	return out, nil
}

// ListMessages retrieves messages matching the filter.
func (m *MockStorage) ListMessages(ctx context.Context, filter MessageFilter) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	var candidates []*Message
	if filter.ThreadID != "" {
		for _, msgID := range m.threadMsgs[filter.ThreadID] {
			if msg, ok := m.messages[msgID]; ok {
				candidates = append(candidates, msg)
			}
		}
	} else {
		for _, msg := range m.messages {
			candidates = append(candidates, msg)
		}
	}

	var results []*Message
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
	sortAsc := filter.SortOrder != SortDesc
	slices.SortStableFunc(results, func(a, b *Message) int {
		cmp := a.CreatedAt.Compare(b.CreatedAt)
		if !sortAsc {
			cmp = -cmp
		}
		return cmp
	})

	// Pagination
	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*Message{}, nil
		}
		results = results[filter.Offset:]
	}
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// GetMessagesByID retrieves messages by their IDs.
func (m *MockStorage) GetMessagesByID(ctx context.Context, ids []string) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	var results []*Message
	for _, id := range ids {
		if msg, ok := m.messages[id]; ok {
			results = append(results, msg.Clone())
		}
	}
	return results, nil
}

// DeleteMessages removes messages by their IDs.
func (m *MockStorage) DeleteMessages(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	for _, id := range ids {
		if msg, ok := m.messages[id]; ok {
			delete(m.messages, id)
			if list, exists := m.threadMsgs[msg.ThreadID]; exists {
				m.threadMsgs[msg.ThreadID] = slices.DeleteFunc(list, func(item string) bool {
					return item == id
				})
			}
		}
	}
	return nil
}

// GetThreadState retrieves the value for (threadID, stateType).
func (m *MockStorage) GetThreadState(ctx context.Context, threadID string, stateType string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if threadID == "" || stateType == "" {
		return nil, fmt.Errorf("%w: threadID and stateType cannot be empty", ErrInvalidInput)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	byType, ok := m.threadStates[threadID]
	if !ok {
		return nil, ErrStateNotFound
	}
	record, ok := byType[stateType]
	if !ok {
		return nil, ErrStateNotFound
	}
	return record.Value, nil
}

// SetThreadState sets the state value for (threadID, stateType).
func (m *MockStorage) SetThreadState(ctx context.Context, threadID string, stateType string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", ErrInvalidInput)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	byType, ok := m.threadStates[threadID]
	if !ok {
		byType = make(map[string]*ThreadState)
		m.threadStates[threadID] = byType
	}
	byType[stateType] = &ThreadState{
		ThreadID:  threadID,
		Type:      stateType,
		Value:     value,
		UpdatedAt: time.Now().UTC(),
	}
	return nil
}

// DeleteThreadState removes the state for (threadID, stateType).
func (m *MockStorage) DeleteThreadState(ctx context.Context, threadID string, stateType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", ErrInvalidInput)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	if byType, ok := m.threadStates[threadID]; ok {
		delete(byType, stateType)
		if len(byType) == 0 {
			delete(m.threadStates, threadID)
		}
	}
	return nil
}

// ListThreadStates lists all states for a thread.
func (m *MockStorage) ListThreadStates(ctx context.Context, threadID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if threadID == "" {
		return nil, fmt.Errorf("%w: threadID cannot be empty", ErrInvalidInput)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}

	byType, ok := m.threadStates[threadID]
	if !ok {
		return make(map[string]any), nil
	}
	res := make(map[string]any, len(byType))
	for k, v := range byType {
		res[k] = v.Value
	}
	return res, nil
}

// Close closes the mock storage backend.
func (m *MockStorage) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Ensure MockStorage satisfies Storage and MemoryStore at compile time.
var (
	_ Storage     = (*MockStorage)(nil)
	_ MemoryStore = (*MockStorage)(nil)
)
