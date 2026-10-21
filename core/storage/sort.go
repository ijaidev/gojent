package storage

// SortOrder defines the sorting order for storage queries.
type SortOrder string

const (
	// SortAsc specifies ascending sort order (oldest first).
	SortAsc SortOrder = "asc"
	// SortDesc specifies descending sort order (newest first).
	SortDesc SortOrder = "desc"
)

// ThreadSortField defines the fields by which threads can be sorted.
type ThreadSortField string

const (
	// ThreadSortUpdatedAt sorts threads by their last update timestamp.
	// The zero value sorts by creation timestamp.
	ThreadSortUpdatedAt ThreadSortField = "updated_at"
)
