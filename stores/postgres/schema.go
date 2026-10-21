package postgres

import (
	"context"

	"github.com/ijaidev/gojent/core/storage"
)

// Schema contains the DDL statements for initializing Postgres storage tables and indexes.
const Schema = `
CREATE TABLE IF NOT EXISTS threads (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_threads_resource_id ON threads(resource_id);
CREATE INDEX IF NOT EXISTS idx_threads_created_at ON threads(created_at);
CREATE INDEX IF NOT EXISTS idx_threads_updated_at ON threads(updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    content JSONB NOT NULL DEFAULT '[]'::jsonb,
    thinking TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    tool_calls JSONB NOT NULL DEFAULT '[]'::jsonb,
    tool_call_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    raw JSONB NOT NULL DEFAULT 'null'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_messages_thread_id ON messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_messages_resource_id ON messages(resource_id);
CREATE INDEX IF NOT EXISTS idx_messages_role ON messages(role);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_thread_created ON messages(thread_id, created_at);
`

// Migrate ensures required database tables and indexes exist.
func (s *Store) Migrate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}

	if _, err := s.db.ExecContext(ctx, Schema); err != nil {
		return err
	}

	// CREATE TABLE IF NOT EXISTS cannot evolve existing tables, so add
	// columns introduced after the initial schema.
	_, err := s.db.ExecContext(ctx, "ALTER TABLE messages ADD COLUMN IF NOT EXISTS thinking TEXT NOT NULL DEFAULT ''")
	return err
}
