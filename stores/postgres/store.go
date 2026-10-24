package postgres

import (
	"context"
	"encoding/json"
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
		SELECT message
		FROM session_messages
		WHERE resource_id = $1 AND session_id = $2
		ORDER BY id ASC`,
		sess.ResourceID, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []llm.Message
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("postgres: scan message: %w", err)
		}
		var message llm.Message
		if err := json.Unmarshal(encoded, &message); err != nil {
			return nil, fmt.Errorf("postgres: decode message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate messages: %w", err)
	}
	return messages, nil
}

// Append atomically persists messages in order.
func (s *Store) Append(ctx context.Context, sess session.Session, messages ...llm.Message) error {
	if err := sess.Validate(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return session.ErrNilStoreMessage
	}
	if err := s.available(); err != nil {
		return err
	}

	encoded := make([][]byte, len(messages))
	for i := range messages {
		data, err := json.Marshal(messages[i])
		if err != nil {
			return fmt.Errorf("postgres: encode message %d: %w", i, err)
		}
		encoded[i] = data
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO session_messages (resource_id, session_id, message)
		VALUES ($1, $2, $3)`)
	if err != nil {
		return fmt.Errorf("postgres: prepare append: %w", err)
	}
	defer func() { _ = statement.Close() }()

	for i, data := range encoded {
		if _, err := statement.ExecContext(ctx, sess.ResourceID, sess.ID, data); err != nil {
			return fmt.Errorf("postgres: append message %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: commit append: %w", err)
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
		DELETE FROM session_messages WHERE resource_id = $1 AND session_id = $2`,
		sess.ResourceID, sess.ID); err != nil {
		return fmt.Errorf("postgres: delete session: %w", err)
	}
	return nil
}
