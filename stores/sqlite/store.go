package sqlite

import (
	"context"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

var _ session.Store = (*Store)(nil)

// Load returns a session's messages in append order.
func (s *Store) Load(ctx context.Context, sess session.Session) ([]llm.Message, error) {
	if err := sess.Validate(); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT message, format_version
		FROM session_messages
		WHERE resource_id = ? AND session_id = ?
		ORDER BY position ASC`,
		sess.ResourceID, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []llm.Message
	for rows.Next() {
		var (
			encoded       []byte
			formatVersion int
		)
		if err := rows.Scan(&encoded, &formatVersion); err != nil {
			return nil, fmt.Errorf("sqlite: scan message: %w", err)
		}
		if formatVersion != llm.FormatVersion {
			return nil, fmt.Errorf("sqlite: decode message: %w: got %d, want %d",
				llm.ErrUnsupportedFormatVersion, formatVersion, llm.FormatVersion)
		}
		message, err := llm.UnmarshalMessage(encoded)
		if err != nil {
			return nil, fmt.Errorf("sqlite: decode message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate messages: %w", err)
	}
	return messages, nil
}

// Append atomically persists messages in order.
func (s *Store) Append(ctx context.Context, sess session.Session, messages ...llm.Message) error {
	if err := sess.Validate(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return session.ErrMessagesRequired
	}
	if err := s.available(); err != nil {
		return err
	}

	encoded := make([][]byte, len(messages))
	for i := range messages {
		data, err := llm.MarshalMessage(messages[i])
		if err != nil {
			return fmt.Errorf("sqlite: encode message %d: %w", i, err)
		}
		encoded[i] = data
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO session_messages (resource_id, session_id, format_version, message, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`)
	if err != nil {
		return fmt.Errorf("sqlite: prepare append: %w", err)
	}
	defer func() { _ = statement.Close() }()

	for i, data := range encoded {
		if _, err := statement.ExecContext(ctx, sess.ResourceID, sess.ID, llm.FormatVersion, data); err != nil {
			return fmt.Errorf("sqlite: append message %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit append: %w", err)
	}
	return nil
}

// Delete removes every message in a session. It exists for tests and
// administrative cleanup; the core Store contract does not require it.
func (s *Store) Delete(ctx context.Context, sess session.Session) error {
	if err := sess.Validate(); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM session_messages WHERE resource_id = ? AND session_id = ?`,
		sess.ResourceID, sess.ID); err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}
