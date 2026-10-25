package sqlite

import (
	"context"
	"fmt"
)

// schema creates the canonical session table. The SDK is unreleased, so there
// is no migration from the v1 layout: drop old development tables and let
// Migrate recreate them.
const schema = `
CREATE TABLE IF NOT EXISTS session_messages (
    position       INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id    TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    format_version INTEGER NOT NULL DEFAULT 2,
    message        TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_messages_session
    ON session_messages(resource_id, session_id, position);
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
		return fmt.Errorf("sqlite: apply schema: %w", err)
	}
	return nil
}
