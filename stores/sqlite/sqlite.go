// Package sqlite provides a thread-safe, ACID-compliant SQLite implementation of the Gojent storage interfaces.
// It supports in-memory databases, file-based databases, automatic migrations, transactions, and concurrent access.
package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	_ "github.com/mattn/go-sqlite3" // Register sqlite3 driver for database/sql.
)

// DefaultDriverName is the default SQL driver registered for SQLite.
const DefaultDriverName = "sqlite3"

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(timeFormat, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.DateTime, s)
}

func genID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

const schema = `
CREATE TABLE IF NOT EXISTS threads (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_threads_resource_id ON threads(resource_id);
CREATE INDEX IF NOT EXISTS idx_threads_created_at ON threads(created_at);
CREATE INDEX IF NOT EXISTS idx_threads_updated_at ON threads(updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    tool_calls TEXT NOT NULL DEFAULT '',
    tool_call_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '',
    raw TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_messages_thread_id ON messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_messages_resource_id ON messages(resource_id);
CREATE INDEX IF NOT EXISTS idx_messages_role ON messages(role);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_thread_created ON messages(thread_id, created_at);

CREATE TABLE IF NOT EXISTS thread_state (
    thread_id TEXT NOT NULL,
    type TEXT NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (thread_id, type)
);

CREATE INDEX IF NOT EXISTS idx_thread_state_thread_id ON thread_state(thread_id);
`

// Store implements storage.Storage using SQLite as the persistence backend.
type Store struct {
	mu     sync.RWMutex
	closed bool
	db     *sql.DB
}

type options struct {
	driverName      string
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
	busyTimeout     time.Duration
	autoMigrate     bool
}

// Option configures Store settings.
type Option func(*options)

// WithDriverName configures a custom SQL driver name (defaults to "sqlite3").
func WithDriverName(driverName string) Option {
	return func(o *options) {
		if driverName != "" {
			o.driverName = driverName
		}
	}
}

// WithMaxOpenConns sets the maximum number of open connections to the database.
func WithMaxOpenConns(n int) Option {
	return func(o *options) {
		o.maxOpenConns = n
	}
}

// WithMaxIdleConns sets the maximum number of idle connections in the pool.
func WithMaxIdleConns(n int) Option {
	return func(o *options) {
		o.maxIdleConns = n
	}
}

// WithConnMaxLifetime sets the maximum amount of time a connection may be reused.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(o *options) {
		o.connMaxLifetime = d
	}
}

// WithConnMaxIdleTime sets the maximum amount of time a connection may be idle.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(o *options) {
		o.connMaxIdleTime = d
	}
}

// WithBusyTimeout sets the SQLite busy_timeout duration (defaults to 5s).
func WithBusyTimeout(d time.Duration) Option {
	return func(o *options) {
		o.busyTimeout = d
	}
}

// WithAutoMigrate enables or disables automatic database migration on startup (defaults to true).
func WithAutoMigrate(autoMigrate bool) Option {
	return func(o *options) {
		o.autoMigrate = autoMigrate
	}
}

// New creates and initializes a new SQLite Store from a connection DSN.
// If dsn is empty, an in-memory database (":memory:") is used.
func New(dsn string, opts ...Option) (*Store, error) {
	if dsn == "" {
		dsn = ":memory:"
	}

	cfg := &options{
		driverName:  DefaultDriverName,
		busyTimeout: 5 * time.Second,
		autoMigrate: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	db, err := sql.Open(cfg.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to open database: %w", err)
	}

	// For in-memory databases, restrict connection pool to 1 by default so that
	// separate queries share the exact same in-memory state.
	isMemory := dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
	if isMemory && cfg.maxOpenConns == 0 {
		db.SetMaxOpenConns(1)
	} else if cfg.maxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.maxOpenConns)
	}
	if cfg.maxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.maxIdleConns)
	}
	if cfg.connMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.connMaxLifetime)
	}
	if cfg.connMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.connMaxIdleTime)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Configure PRAGMAs
	busyTimeoutMs := int(cfg.busyTimeout.Milliseconds())
	if busyTimeoutMs <= 0 {
		busyTimeoutMs = 5000
	}
	pragmaSQL := fmt.Sprintf(`
		PRAGMA busy_timeout = %d;
		PRAGMA foreign_keys = ON;
		PRAGMA synchronous = NORMAL;
	`, busyTimeoutMs)
	if !isMemory {
		pragmaSQL += "PRAGMA journal_mode = WAL;\n"
	}
	if _, err := db.ExecContext(ctx, pragmaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: failed to set pragmas: %w", err)
	}

	store := &Store{db: db}

	if cfg.autoMigrate {
		if err := store.Migrate(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite: failed to apply migrations: %w", err)
		}
	}

	return store, nil
}

// NewWithDB creates a Store wrapping an existing *sql.DB instance.
func NewWithDB(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db cannot be nil", storage.ErrInvalidInput)
	}

	cfg := &options{
		autoMigrate: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	store := &Store{db: db}

	if cfg.autoMigrate {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.Migrate(ctx); err != nil {
			return nil, fmt.Errorf("sqlite: failed to apply migrations: %w", err)
		}
	}

	return store, nil
}

// DB returns the underlying *sql.DB handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Migrate ensures required database tables and indexes exist.
func (s *Store) Migrate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}

	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Store) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// Close closes the database connection pool. Subsequent operations return storage.ErrClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// Clear removes all rows from threads, messages, and thread_state.
func (s *Store) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM thread_state"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM threads"); err != nil {
		return err
	}

	return tx.Commit()
}

// DangerouslyClearAll removes all stored threads, messages, and state using a background context.
func (s *Store) DangerouslyClearAll() {
	_ = s.Clear(context.Background())
}

// SaveThread creates or updates a conversation thread in SQLite.
func (s *Store) SaveThread(ctx context.Context, thread *storage.Thread) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if thread == nil {
		return nil, fmt.Errorf("%w: thread cannot be nil", storage.ErrInvalidInput)
	}

	th := thread.Clone()
	if th.ID == "" {
		th.ID = genID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		var oldCreatedStr string
		err := s.db.QueryRowContext(ctx, "SELECT created_at FROM threads WHERE id = ?", th.ID).Scan(&oldCreatedStr)
		if err == nil {
			if t, parseErr := parseTime(oldCreatedStr); parseErr == nil && !t.IsZero() {
				th.CreatedAt = t
			} else {
				th.CreatedAt = now
			}
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now

	var metadataStr string
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataStr = string(b)
	}

	query := `
		INSERT INTO threads (id, title, resource_id, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			resource_id = excluded.resource_id,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata
	`
	_, err := s.db.ExecContext(ctx, query,
		th.ID,
		th.Title,
		th.ResourceID,
		formatTime(th.CreatedAt),
		formatTime(th.UpdatedAt),
		metadataStr,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to save thread: %w", err)
	}

	return th.Clone(), nil
}

// GetThreadByID retrieves a conversation thread by its unique ID.
func (s *Store) GetThreadByID(ctx context.Context, id string) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	var (
		th                                         storage.Thread
		createdAtStr, updatedAtStr, rawMetadataStr string
	)

	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = ?"
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&th.ID,
		&th.Title,
		&th.ResourceID,
		&createdAtStr,
		&updatedAtStr,
		&rawMetadataStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to get thread: %w", err)
	}

	th.CreatedAt, _ = parseTime(createdAtStr)
	th.UpdatedAt, _ = parseTime(updatedAtStr)
	if rawMetadataStr != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
			th.Metadata = meta
		}
	}

	return th.Clone(), nil
}

// UpdateThread updates specific attributes of an existing thread.
func (s *Store) UpdateThread(ctx context.Context, id string, input storage.UpdateThreadInput) (*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}
	if id == "" {
		return nil, storage.ErrThreadRequired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		title, resourceID, createdAtStr, updatedAtStr, rawMetadataStr string
	)
	query := "SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = ?"
	err = tx.QueryRowContext(ctx, query, id).Scan(&title, &resourceID, &createdAtStr, &updatedAtStr, &rawMetadataStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to query thread for update: %w", err)
	}

	th := &storage.Thread{
		ID:         id,
		Title:      title,
		ResourceID: resourceID,
	}
	th.CreatedAt, _ = parseTime(createdAtStr)
	th.UpdatedAt, _ = parseTime(updatedAtStr)
	if rawMetadataStr != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
			th.Metadata = meta
		}
	}

	if input.Title != nil {
		th.Title = *input.Title
	}
	if input.ResourceID != nil {
		th.ResourceID = *input.ResourceID
	}
	if input.Metadata != nil {
		if th.Metadata == nil {
			th.Metadata = make(map[string]any, len(input.Metadata))
		}
		maps.Copy(th.Metadata, input.Metadata)
	}
	th.UpdatedAt = time.Now().UTC()

	var newMetadataStr string
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		newMetadataStr = string(b)
	}

	updateQuery := "UPDATE threads SET title = ?, resource_id = ?, updated_at = ?, metadata = ? WHERE id = ?"
	_, err = tx.ExecContext(ctx, updateQuery, th.Title, th.ResourceID, formatTime(th.UpdatedAt), newMetadataStr, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to update thread: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return th.Clone(), nil
}

// DeleteThread removes a conversation thread and cascades deletion to all messages and thread state.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isClosed() {
		return storage.ErrClosed
	}
	if id == "" {
		return storage.ErrThreadRequired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, "DELETE FROM threads WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: failed to delete thread: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return storage.ErrThreadNotFound
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE thread_id = ?", id); err != nil {
		return fmt.Errorf("sqlite: failed to cascade delete messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM thread_state WHERE thread_id = ?", id); err != nil {
		return fmt.Errorf("sqlite: failed to cascade delete thread_state: %w", err)
	}

	return tx.Commit()
}

// ListThreads retrieves conversation threads matching the filter criteria.
func (s *Store) ListThreads(ctx context.Context, filter storage.ThreadFilter) ([]*storage.Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, storage.ErrClosed
	}

	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads"
	var whereClauses []string
	var args []any

	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, "resource_id = ?")
		args = append(args, filter.ResourceID)
	}

	if len(filter.Metadata) > 0 {
		for k, v := range filter.Metadata {
			escapedKey := strings.ReplaceAll(k, `\`, `\\`)
			escapedKey = strings.ReplaceAll(escapedKey, `"`, `\"`)
			path := fmt.Sprintf(`$."%s"`, escapedKey)
			if v == nil {
				whereClauses = append(whereClauses, "metadata != '' AND json_extract(NULLIF(metadata, ''), ?) IS NULL")
				args = append(args, path)
			} else {
				switch val := v.(type) {
				case bool:
					intVal := 0
					if val {
						intVal = 1
					}
					whereClauses = append(whereClauses, "json_extract(NULLIF(metadata, ''), ?) = ?")
					args = append(args, path, intVal)
				case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
					whereClauses = append(whereClauses, "json_extract(NULLIF(metadata, ''), ?) = ?")
					args = append(args, path, val)
				default:
					jsonBytes, err := json.Marshal(val)
					if err != nil {
						return nil, fmt.Errorf("%w: failed to marshal metadata filter: %w", storage.ErrInvalidInput, err)
					}
					whereClauses = append(whereClauses, "CASE WHEN json_valid(json_extract(NULLIF(metadata, ''), ?)) = 1 THEN json(json_extract(NULLIF(metadata, ''), ?)) = json(?) ELSE 0 END")
					args = append(args, path, path, string(jsonBytes))
				}
			}
		}
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	sortCol := "created_at"
	if filter.SortBy == storage.ThreadSortUpdatedAt {
		sortCol = "updated_at"
	}
	sortDir := "ASC"
	if filter.SortOrder == storage.SortDesc {
		sortDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s, id %s", sortCol, sortDir, sortDir)

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
		return nil, fmt.Errorf("sqlite: failed to list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Thread, 0)
	for rows.Next() {
		var (
			th                                         storage.Thread
			createdAtStr, updatedAtStr, rawMetadataStr string
		)
		if err := rows.Scan(&th.ID, &th.Title, &th.ResourceID, &createdAtStr, &updatedAtStr, &rawMetadataStr); err != nil {
			return nil, fmt.Errorf("sqlite: failed to scan thread: %w", err)
		}
		th.CreatedAt, _ = parseTime(createdAtStr)
		th.UpdatedAt, _ = parseTime(updatedAtStr)
		if rawMetadataStr != "" {
			var meta map[string]any
			if err := json.Unmarshal([]byte(rawMetadataStr), &meta); err == nil {
				th.Metadata = meta
			}
		}
		results = append(results, th.Clone())
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: thread iteration error: %w", err)
	}

	return results, nil
}

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
		INSERT INTO messages (id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			resource_id = excluded.resource_id,
			role = excluded.role,
			content = excluded.content,
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

		out = append(out, mc.Clone())
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

	query := "SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages"
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
		query := fmt.Sprintf("SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN (%s)", placeholders)
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

// GetThreadState retrieves the durable state value for a (threadID, stateType) pair.
func (s *Store) GetThreadState(ctx context.Context, threadID string, stateType string) (any, error) {
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
func (s *Store) SetThreadState(ctx context.Context, threadID string, stateType string, value any) error {
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
func (s *Store) DeleteThreadState(ctx context.Context, threadID string, stateType string) error {
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

// Compile-time checks that Store implements storage.Storage and all domain store interfaces.
var (
	_ storage.Storage          = (*Store)(nil)
	_ storage.MemoryStore      = (*Store)(nil)
	_ storage.ThreadStore      = (*Store)(nil)
	_ storage.MessageStore     = (*Store)(nil)
	_ storage.ThreadStateStore = (*Store)(nil)
)
