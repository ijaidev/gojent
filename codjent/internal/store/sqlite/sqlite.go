// Package sqlite implements codjent's Store on SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/core/llm"

	// Register the pure-Go SQLite driver with database/sql.
	_ "modernc.org/sqlite"
)

const timeLayout = "2006-01-02T15:04:05.000000000Z"

// schema stores the canonical llm.Message JSON with its format version. The
// SDK is unreleased, so there is no migration from the v1 layout: New rejects
// old development databases and the operator drops them.
var schema = fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS threads (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    access_mode TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    format_version INTEGER NOT NULL DEFAULT %d,
    message TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    format TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS provider_models (
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (provider_id, model_id)
);

CREATE TABLE IF NOT EXISTS provider_headers (
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    value TEXT NOT NULL,
    dynamic INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, name)
);

CREATE INDEX IF NOT EXISTS idx_threads_updated_at ON threads(updated_at);
CREATE INDEX IF NOT EXISTS idx_messages_thread_seq ON messages(thread_id, seq);
CREATE INDEX IF NOT EXISTS idx_threads_provider ON threads(provider);
CREATE INDEX IF NOT EXISTS idx_provider_models_position ON provider_models(provider_id, position);
CREATE INDEX IF NOT EXISTS idx_provider_headers_position ON provider_headers(provider_id, position);
`, llm.FormatVersion)

// Store is a SQLite-backed app.Store.
type Store struct {
	db *sql.DB
}

var _ app.Store = (*Store)(nil)

// New opens (or creates) a SQLite database and applies the schema.
func New(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: database path is required")
	}

	inMemory := path == ":memory:"
	if !inMemory {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("sqlite: resolve database path: %w", err)
		}
		path = absolute
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("sqlite: create data directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("sqlite: create database file: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("sqlite: close database file: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("sqlite: protect database file: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dataSourceName(path, inMemory))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: connect: %w", err)
	}
	if err := ensureCompatibleSchema(ctx, db, path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return store, nil
}

// ensureCompatibleSchema fails fast when an existing database predates the v2
// message layout. The v1 rows cannot be decoded into canonical messages, so the
// operator must remove the file instead of hitting a missing-column error at
// query time.
func ensureCompatibleSchema(ctx context.Context, db *sql.DB, path string) error {
	var columns int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('messages')`).Scan(&columns)
	if err != nil {
		return fmt.Errorf("sqlite: inspect messages table: %w", err)
	}
	if columns == 0 {
		return nil
	}

	var hasFormatVersion int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'format_version'`).Scan(&hasFormatVersion)
	if err != nil {
		return fmt.Errorf("sqlite: inspect messages table: %w", err)
	}
	if hasFormatVersion == 0 {
		return fmt.Errorf("sqlite: %q uses the v1 message layout, which is not migrated: move or delete the file", path)
	}
	return nil
}

func dataSourceName(path string, inMemory bool) string {
	if inMemory {
		return "file::memory:?cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	u.RawQuery = q.Encode()
	return u.String()
}

// CreateThread implements app.Store.
func (s *Store) CreateThread(ctx context.Context, thread *app.Thread) error {
	if thread == nil {
		return fmt.Errorf("%w: thread is nil", app.ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO threads (id, title, provider, model, access_mode, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		thread.ID, thread.Title, thread.Provider, thread.Model, string(thread.AccessMode),
		formatTime(thread.CreatedAt), formatTime(thread.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: create thread: %w", err)
	}
	return nil
}

// GetThread implements app.Store.
func (s *Store) GetThread(ctx context.Context, id string) (*app.Thread, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, provider, model, access_mode, created_at, updated_at
		FROM threads WHERE id = ?`, id)
	thread, err := scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get thread: %w", err)
	}
	return thread, nil
}

// ListThreads implements app.Store.
func (s *Store) ListThreads(ctx context.Context) ([]*app.Thread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, provider, model, access_mode, created_at, updated_at
		FROM threads ORDER BY updated_at DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var threads []*app.Thread
	threads = make([]*app.Thread, 0)
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan thread: %w", err)
		}
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list threads: %w", err)
	}
	return threads, nil
}

// UpdateThread implements app.Store.
func (s *Store) UpdateThread(ctx context.Context, thread *app.Thread) error {
	if thread == nil {
		return fmt.Errorf("%w: thread is nil", app.ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE threads
		SET title = ?, provider = ?, model = ?, access_mode = ?, updated_at = ?
		WHERE id = ?`,
		thread.Title, thread.Provider, thread.Model, string(thread.AccessMode),
		formatTime(thread.UpdatedAt), thread.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update thread: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update thread: %w", err)
	}
	if affected == 0 {
		return app.ErrThreadNotFound
	}
	return nil
}

// DeleteThread implements app.Store.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM threads WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete thread: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete thread: %w", err)
	}
	if affected == 0 {
		return app.ErrThreadNotFound
	}
	return nil
}

// AppendMessages implements app.Store.
func (s *Store) AppendMessages(ctx context.Context, messages ...*app.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if messages[0] == nil {
		return fmt.Errorf("%w: message is nil", app.ErrInvalidInput)
	}
	threadID := messages[0].ThreadID
	if threadID == "" {
		return fmt.Errorf("%w: thread ID is required", app.ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM threads WHERE id = ?`, threadID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrThreadNotFound
		}
		return fmt.Errorf("sqlite: find thread: %w", err)
	}

	for _, message := range messages {
		if message == nil || message.ID == "" {
			return fmt.Errorf("%w: message and message ID are required", app.ErrInvalidInput)
		}
		if message.ThreadID != threadID {
			return fmt.Errorf("%w: messages span multiple threads", app.ErrInvalidInput)
		}
		if err := message.Payload.Validate(); err != nil {
			return fmt.Errorf("%w: message %q: %w", app.ErrInvalidInput, message.ID, err)
		}

		payload, err := llm.MarshalMessage(message.Payload)
		if err != nil {
			return fmt.Errorf("sqlite: encode message %q: %w", message.ID, err)
		}
		createdAt := message.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (id, thread_id, format_version, message, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			message.ID, message.ThreadID, llm.FormatVersion, string(payload),
			formatTime(createdAt),
		); err != nil {
			return fmt.Errorf("sqlite: append message: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), threadID)
	if err != nil {
		return fmt.Errorf("sqlite: touch thread: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: touch thread: %w", err)
	}
	if affected == 0 {
		return app.ErrThreadNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit append: %w", err)
	}
	return nil
}

// ListMessages implements app.Store.
func (s *Store) ListMessages(ctx context.Context, threadID string) ([]*app.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, thread_id, format_version, message, created_at
		FROM messages WHERE thread_id = ? ORDER BY seq ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*app.Message
	messages = make([]*app.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list messages: %w", err)
	}
	return messages, nil
}

// Close implements app.Store.
func (s *Store) Close() error {
	return s.db.Close()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanThread(row scanner) (*app.Thread, error) {
	var (
		thread     app.Thread
		accessMode string
		createdAt  string
		updatedAt  string
	)
	if err := row.Scan(
		&thread.ID, &thread.Title, &thread.Provider, &thread.Model, &accessMode,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	thread.AccessMode = app.AccessMode(accessMode)
	thread.CreatedAt = parseTime(createdAt)
	thread.UpdatedAt = parseTime(updatedAt)
	return &thread, nil
}

func scanMessage(row scanner) (*app.Message, error) {
	var (
		message       app.Message
		formatVersion int
		payload       string
		createdAt     string
	)
	if err := row.Scan(
		&message.ID, &message.ThreadID, &formatVersion, &payload, &createdAt,
	); err != nil {
		return nil, err
	}
	if formatVersion != llm.FormatVersion {
		return nil, fmt.Errorf("%w: got %d, want %d",
			llm.ErrUnsupportedFormatVersion, formatVersion, llm.FormatVersion)
	}
	decoded, err := llm.UnmarshalMessage([]byte(payload))
	if err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	message.Payload = decoded
	message.CreatedAt = parseTime(createdAt)
	return &message, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format(timeLayout)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(timeLayout, raw); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}
