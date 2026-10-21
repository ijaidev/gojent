package storage

import (
	"maps"
	"time"
)

// Thread represents a conversation thread or interaction session in Gojent.
// It groups messages together and can be scoped by resource (e.g. user, workspace, or agent).
type Thread struct {
	// ID is the unique identifier of the thread.
	ID string `json:"id"`
	// Title is an optional human-readable title or topic for the thread.
	Title string `json:"title,omitempty"`
	// ResourceID is an optional identifier for the owner (user, tenant, or agent) scoping this thread.
	ResourceID string `json:"resource_id,omitempty"`
	// CreatedAt is the timestamp when the thread was initially created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp when the thread or its metadata was last updated.
	UpdatedAt time.Time `json:"updated_at"`
	// Metadata contains arbitrary key-value pairs associated with the thread.
	// Nested values are treated as immutable.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Clone returns a copy of the Thread with an independent top-level metadata map.
// Nested metadata values are shared and should be treated as immutable.
func (t *Thread) Clone() *Thread {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Metadata = maps.Clone(t.Metadata)
	return &clone
}

// UpdateThreadInput defines options for partially updating a thread.
// Nil fields represent untouched columns/attributes.
type UpdateThreadInput struct {
	// Title if non-nil updates the thread's title.
	Title *string `json:"title,omitempty"`
	// Metadata if non-nil merges its keys into the thread's existing metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ThreadFilter specifies criteria for filtering and paginating threads in queries.
type ThreadFilter struct {
	// ResourceID filters threads owned by a specific resource/user/tenant.
	ResourceID string `json:"resource_id,omitempty"`
	// Metadata specifies key-value pairs that must match in thread metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Limit is the maximum number of threads to return (0 means unlimited / default).
	Limit int `json:"limit,omitempty"`
	// Offset is the number of initial records to skip for pagination.
	Offset int `json:"offset,omitempty"`
	// SortBy selects the field to sort by (defaults to creation time).
	SortBy ThreadSortField `json:"sort_by,omitempty"`
	// SortOrder selects the order of sorting (defaults to SortAsc).
	SortOrder SortOrder `json:"sort_order,omitempty"`
}
