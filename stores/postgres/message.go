package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

// SaveMessages persists one or more messages to PostgreSQL within a single transaction.
func (s *Store) SaveMessages(ctx context.Context, messages []*storage.Message) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if len(messages) == 0 {
		return []*storage.Message{}, nil
	}

	for _, msg := range messages {
		if msg == nil {
			return nil, fmt.Errorf("%w: message cannot be nil", storage.ErrInvalidInput)
		}
		if msg.ThreadID == "" {
			return nil, fmt.Errorf("%w: message requires thread_id", storage.ErrThreadRequired)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	out := make([]*storage.Message, 0, len(messages))
	parentResIDs := make(map[string]string)
	now := time.Now().UTC()

	upsertQuery := `
		INSERT INTO messages (id, thread_id, resource_id, role, content, thinking, name, tool_calls, tool_call_id, created_at, metadata, raw)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8::jsonb, $9, $10, $11::jsonb, $12::jsonb)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = EXCLUDED.thread_id,
			resource_id = EXCLUDED.resource_id,
			role = EXCLUDED.role,
			content = EXCLUDED.content,
			thinking = EXCLUDED.thinking,
			name = EXCLUDED.name,
			tool_calls = EXCLUDED.tool_calls,
			tool_call_id = EXCLUDED.tool_call_id,
			created_at = EXCLUDED.created_at,
			metadata = EXCLUDED.metadata,
			raw = EXCLUDED.raw
	`

	for _, msg := range messages {
		mc := msg.Clone()
		isNew := mc.ID == ""
		if isNew {
			mc.ID = genID("msg")
		}

		if mc.CreatedAt.IsZero() {
			if !isNew {
				var oldCreatedAt time.Time
				err := tx.QueryRowContext(ctx, "SELECT created_at FROM messages WHERE id = $1", mc.ID).Scan(scanTime(&oldCreatedAt))
				if err == nil && !oldCreatedAt.IsZero() {
					mc.CreatedAt = oldCreatedAt
				} else {
					mc.CreatedAt = now
				}
			} else {
				mc.CreatedAt = now
			}
		}

		if mc.ResourceID == "" {
			if resID, ok := parentResIDs[mc.ThreadID]; ok {
				mc.ResourceID = resID
			} else {
				var resID string
				err := tx.QueryRowContext(ctx, "SELECT resource_id FROM threads WHERE id = $1", mc.ThreadID).Scan(&resID)
				if err == nil {
					parentResIDs[mc.ThreadID] = resID
					mc.ResourceID = resID
				}
			}
		}

		toolCallsBytes := []byte("[]")
		if mc.ToolCalls != nil {
			b, err := json.Marshal(mc.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal tool_calls: %w", storage.ErrInvalidInput, err)
			}
			toolCallsBytes = b
		}

		metadataBytes := []byte("{}")
		if mc.Metadata != nil {
			b, err := json.Marshal(mc.Metadata)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
			}
			metadataBytes = b
		}

		rawBytes := []byte("null")
		if mc.Raw != nil {
			b, err := json.Marshal(mc.Raw)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal raw payload: %w", storage.ErrInvalidInput, err)
			}
			rawBytes = b
		}

		var contentBytes []byte
		if mc.Content != nil {
			b, err := json.Marshal(mc.Content)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal content: %w", storage.ErrInvalidInput, err)
			}
			contentBytes = b
		} else {
			contentBytes = []byte("[]")
		}

		_, err := tx.ExecContext(ctx, upsertQuery,
			mc.ID,
			mc.ThreadID,
			mc.ResourceID,
			string(mc.Role),
			string(contentBytes),
			mc.Thinking,
			mc.Name,
			string(toolCallsBytes),
			mc.ToolCallID,
			mc.CreatedAt,
			string(metadataBytes),
			string(rawBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: failed to save message %q: %w", mc.ID, err)
		}

		_, _ = tx.ExecContext(ctx, "UPDATE threads SET updated_at = $1 WHERE id = $2", mc.CreatedAt, mc.ThreadID)

		out = append(out, mc.Clone())
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return out, nil
}

func scanMessage(scanner interface{ Scan(dest ...any) error }) (*storage.Message, error) {
	var (
		msg     storage.Message
		roleStr string
	)

	err := scanner.Scan(
		&msg.ID,
		&msg.ThreadID,
		&msg.ResourceID,
		&roleStr,
		scanContent(&msg.Content),
		&msg.Thinking,
		&msg.Name,
		scanJSON(&msg.ToolCalls),
		&msg.ToolCallID,
		scanTime(&msg.CreatedAt),
		scanJSON(&msg.Metadata),
		scanJSON(&msg.Raw),
	)
	if err != nil {
		return nil, err
	}

	msg.Role = llm.Role(roleStr)
	return msg.Clone(), nil
}

// ListMessages retrieves messages matching the filter criteria.
func (s *Store) ListMessages(ctx context.Context, filter storage.MessageFilter) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}

	query := "SELECT id, thread_id, resource_id, role, content, thinking, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages"
	var whereClauses []string
	var args []any
	argIndex := 1

	if filter.ThreadID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("thread_id = $%d", argIndex))
		args = append(args, filter.ThreadID)
		argIndex++
	}
	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("resource_id = $%d", argIndex))
		args = append(args, filter.ResourceID)
		argIndex++
	}
	if filter.Role != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("role = $%d", argIndex))
		args = append(args, string(filter.Role))
		argIndex++
	}
	if filter.Since != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at >= $%d", argIndex))
		args = append(args, *filter.Since)
		argIndex++
	}
	if filter.Until != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at <= $%d", argIndex))
		args = append(args, *filter.Until)
		argIndex++
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	sortDir := "ASC"
	if filter.SortOrder == storage.SortDesc {
		sortDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY created_at %s, id %s", sortDir, sortDir)

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
		return nil, fmt.Errorf("postgres: failed to list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Message, 0)
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: failed to scan message: %w", err)
		}
		results = append(results, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: message iteration error: %w", err)
	}

	return results, nil
}

// GetMessagesByID retrieves messages matching the provided IDs, preserving requested ID order.
func (s *Store) GetMessagesByID(ctx context.Context, ids []string) ([]*storage.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if len(ids) == 0 {
		return []*storage.Message{}, nil
	}

	msgMap := make(map[string]*storage.Message, len(ids))
	chunkSize := 500

	for i := 0; i < len(ids); i += chunkSize {
		end := i + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for j, id := range chunk {
			placeholders[j] = fmt.Sprintf("$%d", j+1)
			args[j] = id
		}

		query := fmt.Sprintf("SELECT id, thread_id, resource_id, role, content, thinking, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN (%s)", strings.Join(placeholders, ", "))

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("postgres: failed to get messages by id: %w", err)
		}

		for rows.Next() {
			msg, err := scanMessage(rows)
			if err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("postgres: scan error in get messages: %w", err)
			}
			msgMap[msg.ID] = msg
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("postgres: row iteration error: %w", err)
		}
	}

	results := make([]*storage.Message, 0, len(ids))
	for _, id := range ids {
		if msg, ok := msgMap[id]; ok {
			results = append(results, msg.Clone())
		}
	}

	return results, nil
}

// DeleteMessages removes messages matching the given IDs.
func (s *Store) DeleteMessages(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	chunkSize := 500
	for i := 0; i < len(ids); i += chunkSize {
		end := i + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for j, id := range chunk {
			placeholders[j] = fmt.Sprintf("$%d", j+1)
			args[j] = id
		}

		query := fmt.Sprintf("DELETE FROM messages WHERE id IN (%s)", strings.Join(placeholders, ", "))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("postgres: failed to delete messages: %w", err)
		}
	}

	return tx.Commit()
}
