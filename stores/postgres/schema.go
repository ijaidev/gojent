package postgres

import (
	"context"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS session_messages (
    id          BIGSERIAL PRIMARY KEY,
    resource_id TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    message     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_session_messages_session
    ON session_messages(resource_id, session_id, id);
`

// Migrate creates the schema. It is idempotent.
func (s *Store) Migrate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("postgres: apply schema: %w", err)
	}
	return nil
}
