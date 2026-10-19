package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ijaidev/gojent/core/storage"
)

// GetThreadState retrieves the durable state value for a (threadID, stateType) pair.
func (s *Store) GetThreadState(ctx context.Context, threadID, stateType string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if threadID == "" || stateType == "" {
		return nil, fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	var val any
	query := "SELECT value FROM thread_state WHERE thread_id = $1 AND type = $2"
	err := s.db.QueryRowContext(ctx, query, threadID, stateType).Scan(scanJSON(&val))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to get thread state: %w", err)
	}

	return val, nil
}

// SetThreadState sets or overwrites the durable state value for a (threadID, stateType) pair.
func (s *Store) SetThreadState(ctx context.Context, threadID, stateType string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: failed to marshal state value: %w", storage.ErrInvalidInput, err)
	}

	query := `
		INSERT INTO thread_state (thread_id, type, value, updated_at)
		VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT(thread_id, type) DO UPDATE SET
			value = EXCLUDED.value,
			updated_at = EXCLUDED.updated_at
	`
	now := normalizeTime(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, query, threadID, stateType, string(b), now)
	if err != nil {
		return fmt.Errorf("postgres: failed to set thread state: %w", err)
	}

	return nil
}

// DeleteThreadState removes the durable state value for a (threadID, stateType) pair.
func (s *Store) DeleteThreadState(ctx context.Context, threadID, stateType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}
	if threadID == "" || stateType == "" {
		return fmt.Errorf("%w: threadID and stateType cannot be empty", storage.ErrInvalidInput)
	}

	query := "DELETE FROM thread_state WHERE thread_id = $1 AND type = $2"
	if _, err := s.db.ExecContext(ctx, query, threadID, stateType); err != nil {
		return fmt.Errorf("postgres: failed to delete thread state: %w", err)
	}

	return nil
}

// ListThreadStates returns all state values recorded for a thread, mapped by state type.
func (s *Store) ListThreadStates(ctx context.Context, threadID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if threadID == "" {
		return nil, fmt.Errorf("%w: threadID cannot be empty", storage.ErrInvalidInput)
	}

	query := "SELECT type, value FROM thread_state WHERE thread_id = $1"
	rows, err := s.db.QueryContext(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to list thread states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	res := make(map[string]any)
	for rows.Next() {
		var (
			stType string
			val    any
		)
		if err := rows.Scan(&stType, scanJSON(&val)); err != nil {
			return nil, fmt.Errorf("postgres: scan error in thread states: %w", err)
		}
		res[stType] = val
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iteration error in thread states: %w", err)
	}

	return res, nil
}
