package sqlite

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

	var rawValue string
	query := "SELECT value FROM thread_state WHERE thread_id = ? AND type = ?"
	err := s.db.QueryRowContext(ctx, query, threadID, stateType).Scan(&rawValue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to get thread state: %w", err)
	}

	if rawValue == "" || rawValue == "null" {
		return nil, nil
	}

	var val any
	if err := json.Unmarshal([]byte(rawValue), &val); err != nil {
		return nil, fmt.Errorf("sqlite: failed to unmarshal state value: %w", err)
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
		VALUES (?, ?, ?, ?)
		ON CONFLICT(thread_id, type) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`
	nowStr := formatTime(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, query, threadID, stateType, string(b), nowStr)
	if err != nil {
		return fmt.Errorf("sqlite: failed to set thread state: %w", err)
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

	query := "DELETE FROM thread_state WHERE thread_id = ? AND type = ?"
	if _, err := s.db.ExecContext(ctx, query, threadID, stateType); err != nil {
		return fmt.Errorf("sqlite: failed to delete thread state: %w", err)
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

	query := "SELECT type, value FROM thread_state WHERE thread_id = ?"
	rows, err := s.db.QueryContext(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to list thread states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	res := make(map[string]any)
	for rows.Next() {
		var stType, rawValue string
		if err := rows.Scan(&stType, &rawValue); err != nil {
			return nil, fmt.Errorf("sqlite: scan error in thread states: %w", err)
		}
		var val any
		if rawValue != "" && rawValue != "null" {
			if err := json.Unmarshal([]byte(rawValue), &val); err == nil {
				res[stType] = val
				continue
			}
		}
		res[stType] = val
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iteration error in thread states: %w", err)
	}

	return res, nil
}
