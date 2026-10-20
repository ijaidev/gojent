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
	// ThreadSortCreatedAt sorts threads by their creation timestamp.
	ThreadSortCreatedAt ThreadSortField = "created_at"
	// ThreadSortUpdatedAt sorts threads by their last update timestamp.
	ThreadSortUpdatedAt ThreadSortField = "updated_at"
)
