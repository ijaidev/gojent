package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/storage"
)

// SaveThread creates or updates a conversation thread in SQLite.
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
	if th.ID == "" {
		th.ID = genID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		var oldCreatedStr string
		err := s.db.QueryRowContext(ctx, "SELECT created_at FROM threads WHERE id = ?", th.ID).Scan(&oldCreatedStr)
		if err == nil {
			if t, parseErr := parseTime(oldCreatedStr); parseErr == nil && !t.IsZero() {
				th.CreatedAt = t
			} else {
				th.CreatedAt = now
			}
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now

	var metadataStr string
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataStr = string(b)
	}

	query := `
		INSERT INTO threads (id, title, resource_id, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			resource_id = excluded.resource_id,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`
	_, err := s.db.ExecContext(ctx, query,
		th.ID,
		th.Title,
		th.ResourceID,
		formatTime(th.CreatedAt),
		formatTime(th.UpdatedAt),
		metadataStr,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to save thread: %w", err)
	}

	return th.Clone(), nil
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

	var (
		th                                         storage.Thread
		createdAtStr, updatedAtStr, rawMetadataStr string
	)

	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = ?"
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&th.ID,
		&th.Title,
		&th.ResourceID,
		&createdAtStr,
		&updatedAtStr,
		&rawMetadataStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to get thread: %w", err)
	}

	th.CreatedAt, _ = parseTime(createdAtStr)
	th.UpdatedAt, _ = parseTime(updatedAtStr)
	if rawMetadataStr != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
			th.Metadata = meta
		}
	}

	return th.Clone(), nil
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

	var (
		title, resourceID, createdAtStr, updatedAtStr, rawMetadataStr string
	)
	query := "SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = ?"
	err = tx.QueryRowContext(ctx, query, id).Scan(&title, &resourceID, &createdAtStr, &updatedAtStr, &rawMetadataStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to query thread for update: %w", err)
	}

	th := &storage.Thread{
		ID:         id,
		Title:      title,
		ResourceID: resourceID,
	}
	th.CreatedAt, _ = parseTime(createdAtStr)
	th.UpdatedAt, _ = parseTime(updatedAtStr)
	if rawMetadataStr != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
			th.Metadata = meta
		}
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

	var newMetadataStr string
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		newMetadataStr = string(b)
	}

	updateQuery := "UPDATE threads SET title = ?, resource_id = ?, updated_at = ?, metadata = ? WHERE id = ?"
	_, err = tx.ExecContext(ctx, updateQuery, th.Title, th.ResourceID, formatTime(th.UpdatedAt), newMetadataStr, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to update thread: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return th.Clone(), nil
}

// DeleteThread removes a conversation thread and cascades deletion to all messages and thread state.
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

	res, err := tx.ExecContext(ctx, "DELETE FROM threads WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: failed to delete thread: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return storage.ErrThreadNotFound
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE thread_id = ?", id); err != nil {
		return fmt.Errorf("sqlite: failed to cascade delete messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM thread_state WHERE thread_id = ?", id); err != nil {
		return fmt.Errorf("sqlite: failed to cascade delete thread_state: %w", err)
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

	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, "resource_id = ?")
		args = append(args, filter.ResourceID)
	}

	if len(filter.Metadata) > 0 {
		for k, v := range filter.Metadata {
			escapedKey := strings.ReplaceAll(k, `\`, `\\`)
			escapedKey = strings.ReplaceAll(escapedKey, `"`, `\"`)
			path := fmt.Sprintf(`$."%s"`, escapedKey)
			if v == nil {
				whereClauses = append(whereClauses, "metadata != '' AND json_extract(NULLIF(metadata, ''), ?) IS NULL")
				args = append(args, path)
			} else {
				switch val := v.(type) {
				case bool:
					intVal := 0
					if val {
						intVal = 1
					}
					whereClauses = append(whereClauses, "json_extract(NULLIF(metadata, ''), ?) = ?")
					args = append(args, path, intVal)
				case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
					whereClauses = append(whereClauses, "json_extract(NULLIF(metadata, ''), ?) = ?")
					args = append(args, path, val)
				default:
					jsonBytes, err := json.Marshal(val)
					if err != nil {
						return nil, fmt.Errorf("%w: failed to marshal metadata filter: %w", storage.ErrInvalidInput, err)
					}
					whereClauses = append(whereClauses, "CASE WHEN json_valid(json_extract(NULLIF(metadata, ''), ?)) = 1 THEN json(json_extract(NULLIF(metadata, ''), ?)) = json(?) ELSE 0 END")
					args = append(args, path, path, string(jsonBytes))
				}
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
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	} else if filter.Offset > 0 {
		query += " LIMIT -1 OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Thread, 0)
	for rows.Next() {
		var (
			th                                         storage.Thread
			createdAtStr, updatedAtStr, rawMetadataStr string
		)
		if err := rows.Scan(&th.ID, &th.Title, &th.ResourceID, &createdAtStr, &updatedAtStr, &rawMetadataStr); err != nil {
			return nil, fmt.Errorf("sqlite: failed to scan thread: %w", err)
		}
		th.CreatedAt, _ = parseTime(createdAtStr)
		th.UpdatedAt, _ = parseTime(updatedAtStr)
		if rawMetadataStr != "" {
			var meta map[string]any
			if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
				th.Metadata = meta
			}
		}
		results = append(results, th.Clone())
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: thread iteration error: %w", err)
	}

	return results, nil
}
