package storage

import (
	"maps"
	"time"
)

// ThreadState represents durable, per-thread state associated with a specific type namespace.
// Common types include "task" (structured agent task lists), "goal" (agent objective status),
// or custom application working memory.
type ThreadState struct {
	// ThreadID identifies the thread this state belongs to.
	ThreadID string `json:"thread_id"`
	// Type is the state namespace key (e.g. "task", "goal", "working_memory").
	Type string `json:"type"`
	// Value holds the arbitrary state payload.
	Value any `json:"value"`
	// UpdatedAt indicates when the state was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// Clone returns a copy of the ThreadState record, deep-copying map payloads.
func (s *ThreadState) Clone() *ThreadState {
	if s == nil {
		return nil
	}
	clone := *s
	if m, ok := s.Value.(map[string]any); ok {
		clone.Value = maps.Clone(m)
	}
	return &clone
}
