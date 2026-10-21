package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/storage"
)

// SaveThread creates or updates a conversation thread in PostgreSQL.
func (s *Store) SaveThread(ctx context.Context, thread *storage.Thread) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if thread == nil {
		return nil, fmt.Errorf("%w: thread cannot be nil", storage.ErrInvalidInput)
	}

	th := thread.Clone()
	isNew := th.ID == ""
	if isNew {
		th.ID = genID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		if !isNew {
			var oldCreatedAt time.Time
			err := s.db.QueryRowContext(ctx, "SELECT created_at FROM threads WHERE id = $1", th.ID).Scan(scanTime(&oldCreatedAt))
			if err == nil && !oldCreatedAt.IsZero() {
				th.CreatedAt = oldCreatedAt
			} else {
				th.CreatedAt = now
			}
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now
	th.CreatedAt = normalizeTime(th.CreatedAt)
	th.UpdatedAt = normalizeTime(th.UpdatedAt)

	metadataBytes := []byte("{}")
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataBytes = b
	}

	query := `
		INSERT INTO threads (id, title, resource_id, created_at, updated_at, metadata)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		ON CONFLICT(id) DO UPDATE SET
			title = EXCLUDED.title,
			resource_id = EXCLUDED.resource_id,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			metadata = EXCLUDED.metadata
	`
	_, err := s.db.ExecContext(ctx, query,
		th.ID,
		th.Title,
		th.ResourceID,
		th.CreatedAt,
		th.UpdatedAt,
		string(metadataBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to save thread: %w", err)
	}

	return th, nil
}

// GetThreadByID retrieves a conversation thread by its unique ID.
func (s *Store) GetThreadByID(ctx context.Context, id string) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	var th storage.Thread
	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1"
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&th.ID,
		&th.Title,
		&th.ResourceID,
		scanTime(&th.CreatedAt),
		scanTime(&th.UpdatedAt),
		scanJSON(&th.Metadata),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to get thread: %w", err)
	}

	return &th, nil
}

// UpdateThread updates specific attributes of an existing thread.
func (s *Store) UpdateThread(ctx context.Context, id string, input storage.UpdateThreadInput) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var th storage.Thread
	th.ID = id
	query := "SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1"
	err = tx.QueryRowContext(ctx, query, id).Scan(
		&th.Title,
		&th.ResourceID,
		scanTime(&th.CreatedAt),
		scanTime(&th.UpdatedAt),
		scanJSON(&th.Metadata),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to query thread for update: %w", err)
	}

	if input.Title != nil {
		th.Title = *input.Title
	}
	if input.Metadata != nil {
		if th.Metadata == nil {
			th.Metadata = make(map[string]any, len(input.Metadata))
		}
		maps.Copy(th.Metadata, input.Metadata)
	}
	th.UpdatedAt = normalizeTime(time.Now().UTC())

	metadataBytes := []byte("{}")
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataBytes = b
	}

	updateQuery := "UPDATE threads SET title = $1, resource_id = $2, updated_at = $3, metadata = $4::jsonb WHERE id = $5"
	_, err = tx.ExecContext(ctx, updateQuery, th.Title, th.ResourceID, th.UpdatedAt, string(metadataBytes), id)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to update thread: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &th, nil
}

// DeleteThread removes a conversation thread and cascades deletion to all messages.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}
	if id == "" {
		return storage.ErrThreadRequired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, "DELETE FROM threads WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("postgres: failed to delete thread: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return storage.ErrThreadNotFound
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE thread_id = $1", id); err != nil {
		return fmt.Errorf("postgres: failed to cascade delete messages: %w", err)
	}
	return tx.Commit()
}

// ListThreads retrieves conversation threads matching the filter criteria.
func (s *Store) ListThreads(ctx context.Context, filter storage.ThreadFilter) ([]*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}

	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads"
	var whereClauses []string
	var args []any
	argIndex := 1

	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("resource_id = $%d", argIndex))
		args = append(args, filter.ResourceID)
		argIndex++
	}

	if len(filter.Metadata) > 0 {
		keys := make([]string, 0, len(filter.Metadata))
		for k := range filter.Metadata {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		for _, k := range keys {
			v := filter.Metadata[k]
			if v == nil {
				whereClauses = append(whereClauses, fmt.Sprintf(
					"(metadata != '{}'::jsonb AND (metadata->$%d IS NULL OR metadata->$%d = 'null'::jsonb))",
					argIndex, argIndex,
				))
				args = append(args, k)
				argIndex++
			} else {
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("%w: failed to marshal metadata filter: %w", storage.ErrInvalidInput, err)
				}
				whereClauses = append(whereClauses, fmt.Sprintf(
					"metadata->$%d = $%d::jsonb",
					argIndex, argIndex+1,
				))
				args = append(args, k, string(jsonBytes))
				argIndex += 2
			}
		}
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	sortCol := "created_at"
	if filter.SortBy == storage.ThreadSortUpdatedAt {
		sortCol = "updated_at"
	}
	sortDir := "ASC"
	if filter.SortOrder == storage.SortDesc {
		sortDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s, id %s", sortCol, sortDir, sortDir)

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, filter.Limit)
		argIndex++
		if filter.Offset > 0 {
			query += fmt.Sprintf(" OFFSET $%d", argIndex)
			args = append(args, filter.Offset)
		}
	} else if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Thread, 0)
	for rows.Next() {
		var th storage.Thread
		if err := rows.Scan(
			&th.ID,
			&th.Title,
			&th.ResourceID,
			scanTime(&th.CreatedAt),
			scanTime(&th.UpdatedAt),
			scanJSON(&th.Metadata),
		); err != nil {
			return nil, fmt.Errorf("postgres: failed to scan thread: %w", err)
		}
		results = append(results, &th)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: thread iteration error: %w", err)
	}

	return results, nil
}
