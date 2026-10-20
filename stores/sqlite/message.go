package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

// SaveMessages persists one or more messages to SQLite within a single transaction.
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
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			resource_id = excluded.resource_id,
			role = excluded.role,
			content = excluded.content,
			thinking = excluded.thinking,
			name = excluded.name,
			tool_calls = excluded.tool_calls,
			tool_call_id = excluded.tool_call_id,
			created_at = excluded.created_at,
			metadata = excluded.metadata,
			raw = excluded.raw
	`

	for _, msg := range messages {
		mc := msg.Clone()
		if mc.ID == "" {
			mc.ID = genID("msg")
		}

		if mc.CreatedAt.IsZero() {
			var oldCreatedStr string
			err := tx.QueryRowContext(ctx, "SELECT created_at FROM messages WHERE id = ?", mc.ID).Scan(&oldCreatedStr)
			if err == nil {
				if t, parseErr := parseTime(oldCreatedStr); parseErr == nil && !t.IsZero() {
					mc.CreatedAt = t
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
				err := tx.QueryRowContext(ctx, "SELECT resource_id FROM threads WHERE id = ?", mc.ThreadID).Scan(&resID)
				if err == nil {
					parentResIDs[mc.ThreadID] = resID
					mc.ResourceID = resID
				}
			}
		}

		var toolCallsStr string
		if mc.ToolCalls != nil {
			b, err := json.Marshal(mc.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal tool_calls: %w", storage.ErrInvalidInput, err)
			}
			toolCallsStr = string(b)
		}

		var metadataStr string
		if mc.Metadata != nil {
			b, err := json.Marshal(mc.Metadata)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
			}
			metadataStr = string(b)
		}

		var rawStr string
		if mc.Raw != nil {
			b, err := json.Marshal(mc.Raw)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal raw payload: %w", storage.ErrInvalidInput, err)
			}
			rawStr = string(b)
		}

		var contentStr string
		if mc.Content != nil {
			b, err := json.Marshal(mc.Content)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to marshal content: %w", storage.ErrInvalidInput, err)
			}
			contentStr = string(b)
		} else {
			contentStr = "[]"
		}

		_, err := tx.ExecContext(ctx, upsertQuery,
			mc.ID,
			mc.ThreadID,
			mc.ResourceID,
			string(mc.Role),
			contentStr,
			mc.Thinking,
			mc.Name,
			toolCallsStr,
			mc.ToolCallID,
			formatTime(mc.CreatedAt),
			metadataStr,
			rawStr,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlite: failed to save message %q: %w", mc.ID, err)
		}

		_, _ = tx.ExecContext(ctx, "UPDATE threads SET updated_at = ? WHERE id = ?", formatTime(mc.CreatedAt), mc.ThreadID)

		out = append(out, mc)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return out, nil
}

func scanMessage(rows interface {
	Scan(dest ...any) error
}) (*storage.Message, error) {
	var (
		msg                                                                              storage.Message
		roleStr, contentText, toolCallsStr, createdAtStr, rawMetadataStr, rawPayloadText string
	)

	err := rows.Scan(
		&msg.ID,
		&msg.ThreadID,
		&msg.ResourceID,
		&roleStr,
		&contentText,
		&msg.Thinking,
		&msg.Name,
		&toolCallsStr,
		&msg.ToolCallID,
		&createdAtStr,
		&rawMetadataStr,
		&rawPayloadText,
	)
	if err != nil {
		return nil, err
	}

	msg.Role = llm.Role(roleStr)
	msg.CreatedAt, _ = parseTime(createdAtStr)

	if contentText != "" && contentText != "null" {
		var parts []llm.Part
		if err := json.Unmarshal([]byte(contentText), &parts); err == nil {
			msg.Content = parts
		} else {
			msg.Content = []llm.Part{llm.TextPart(contentText)}
		}
	}

	if toolCallsStr != "" && toolCallsStr != "null" {
		var toolCalls []llm.ToolCall
		if err := json.Unmarshal([]byte(toolCallsStr), &toolCalls); err == nil {
			msg.ToolCalls = toolCalls
		}
	}
	if rawMetadataStr != "" && rawMetadataStr != "null" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
			msg.Metadata = meta
		}
	}
	if rawPayloadText != "" && rawPayloadText != "null" {
		var raw any
		if err := json.Unmarshal([]byte(rawPayloadText), &raw); err == nil {
			msg.Raw = raw
		}
	}

	return &msg, nil
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

	if filter.ThreadID != "" {
		whereClauses = append(whereClauses, "thread_id = ?")
		args = append(args, filter.ThreadID)
	}
	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, "resource_id = ?")
		args = append(args, filter.ResourceID)
	}
	if filter.Role != "" {
		whereClauses = append(whereClauses, "role = ?")
		args = append(args, string(filter.Role))
	}
	if filter.Since != nil {
		whereClauses = append(whereClauses, "created_at >= ?")
		args = append(args, formatTime(*filter.Since))
	}
	if filter.Until != nil {
		whereClauses = append(whereClauses, "created_at <= ?")
		args = append(args, formatTime(*filter.Until))
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	sortDir := "ASC"
	if filter.SortOrder == storage.SortDesc {
		sortDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY created_at %s, rowid %s", sortDir, sortDir)

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
		return nil, fmt.Errorf("sqlite: failed to list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Message, 0)
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: failed to scan message: %w", err)
		}
		results = append(results, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: message iteration error: %w", err)
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

		placeholders := strings.Repeat("?,", len(chunk)-1) + "?"
		query := fmt.Sprintf("SELECT id, thread_id, resource_id, role, content, thinking, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN (%s)", placeholders)
		args := make([]any, len(chunk))
		for j, id := range chunk {
			args[j] = id
		}

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlite: failed to get messages by id: %w", err)
		}

		for rows.Next() {
			msg, err := scanMessage(rows)
			if err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("sqlite: scan error in get messages: %w", err)
			}
			msgMap[msg.ID] = msg
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("sqlite: row iteration error: %w", err)
		}
	}

	results := make([]*storage.Message, 0, len(ids))
	for _, id := range ids {
		if msg, ok := msgMap[id]; ok {
			results = append(results, msg)
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

		placeholders := strings.Repeat("?,", len(chunk)-1) + "?"
		query := fmt.Sprintf("DELETE FROM messages WHERE id IN (%s)", placeholders)
		args := make([]any, len(chunk))
		for j, id := range chunk {
			args[j] = id
		}

		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("sqlite: failed to delete messages: %w", err)
		}
	}

	return tx.Commit()
}
