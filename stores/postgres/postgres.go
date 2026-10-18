// Package postgres provides a thread-safe PostgreSQL implementation of the Gojent storage interfaces.
// It supports automatic migrations, transactions, connection pooling, and JSONB persistence.
package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	_ "github.com/lib/pq" // Register postgres driver for database/sql.
)

// DefaultDriverName is the default SQL driver registered for PostgreSQL.
const DefaultDriverName = "postgres"

const timeFormat = "2006-01-02T15:04:05.000000000Z"

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

type timeScanner struct {
	dest *time.Time
}

func (s *timeScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	switch v := src.(type) {
	case time.Time:
		*s.dest = v.UTC()
		return nil
	case *time.Time:
		if v != nil {
			*s.dest = v.UTC()
		}
		return nil
	case string:
		t, err := parseTime(v)
		if err != nil {
			return err
		}
		*s.dest = t
		return nil
	case []byte:
		t, err := parseTime(string(v))
		if err != nil {
			return err
		}
		*s.dest = t
		return nil
	default:
		return fmt.Errorf("postgres: cannot scan %T into time.Time", src)
	}
}

func scanTime(dest *time.Time) *timeScanner {
	return &timeScanner{dest: dest}
}

type jsonScanner struct {
	dest any
}

func (s *jsonScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("postgres: cannot scan %T into JSON", src)
	}
	trimmed := strings.TrimSpace(string(b))
	if len(trimmed) == 0 || trimmed == "null" || trimmed == `""` {
		return nil
	}
	return json.Unmarshal([]byte(trimmed), s.dest)
}

func scanJSON(dest any) *jsonScanner {
	return &jsonScanner{dest: dest}
}

type contentScanner struct {
	dest *[]llm.Part
}

func (s *contentScanner) Scan(src any) error {
	if src == nil {
		return nil
	}
	var str string
	switch v := src.(type) {
	case []byte:
		str = string(v)
	case string:
		str = v
	default:
		return fmt.Errorf("postgres: cannot scan %T into content", src)
	}
	trimmed := strings.TrimSpace(str)
	if len(trimmed) == 0 || trimmed == "null" {
		*s.dest = nil
		return nil
	}
	var parts []llm.Part
	if err := json.Unmarshal([]byte(trimmed), &parts); err == nil {
		*s.dest = parts
		return nil
	}
	*s.dest = []llm.Part{llm.TextPart(str)}
	return nil
}

func scanContent(dest *[]llm.Part) *contentScanner {
	return &contentScanner{dest: dest}
}

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
    content TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS thread_state (
    thread_id TEXT NOT NULL,
    type TEXT NOT NULL,
    value JSONB NOT NULL DEFAULT 'null'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (thread_id, type)
);

CREATE INDEX IF NOT EXISTS idx_thread_state_thread_id ON thread_state(thread_id);
`

// Store implements storage.Storage using PostgreSQL as the persistence backend.
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
	autoMigrate     bool
}

// Option configures Store settings.
type Option func(*options)

// WithDriverName configures a custom SQL driver name (defaults to "postgres").
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

// WithAutoMigrate enables or disables automatic database migration on startup (defaults to true).
func WithAutoMigrate(autoMigrate bool) Option {
	return func(o *options) {
		o.autoMigrate = autoMigrate
	}
}

// New creates and initializes a new PostgreSQL Store from a connection DSN.
func New(dsn string, opts ...Option) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("%w: dsn cannot be empty", storage.ErrInvalidInput)
	}

	cfg := &options{
		driverName:  DefaultDriverName,
		autoMigrate: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	db, err := sql.Open(cfg.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to open database: %w", err)
	}

	if cfg.maxOpenConns > 0 {
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

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: failed to ping database: %w", err)
	}

	store := &Store{db: db}

	if cfg.autoMigrate {
		if err := store.Migrate(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("postgres: failed to apply migrations: %w", err)
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
			return nil, fmt.Errorf("postgres: failed to apply migrations: %w", err)
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

	_, err := s.db.ExecContext(ctx, Schema)
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

// SaveThread creates or updates a conversation thread in PostgreSQL.
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
	isNew := th.ID == ""
	if isNew {
		th.ID = genID("th")
	}
	now := time.Now().UTC()
	if th.CreatedAt.IsZero() {
		if !isNew {
			var oldCreatedAt time.Time
			err := s.db.QueryRowContext(ctx, "SELECT created_at FROM threads WHERE id = $1", th.ID).Scan(scanTime(&oldCreatedAt))
			if err == nil && !oldCreatedAt.IsZero() {
				th.CreatedAt = oldCreatedAt
			} else {
				th.CreatedAt = now
			}
		} else {
			th.CreatedAt = now
		}
	}
	th.UpdatedAt = now

	metadataBytes := []byte("{}")
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataBytes = b
	}

	query := `
		INSERT INTO threads (id, title, resource_id, created_at, updated_at, metadata)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		ON CONFLICT(id) DO UPDATE SET
			title = EXCLUDED.title,
			resource_id = EXCLUDED.resource_id,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			metadata = EXCLUDED.metadata
	`
	_, err := s.db.ExecContext(ctx, query,
		th.ID,
		th.Title,
		th.ResourceID,
		th.CreatedAt,
		th.UpdatedAt,
		string(metadataBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to save thread: %w", err)
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

	var th storage.Thread
	query := "SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1"
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&th.ID,
		&th.Title,
		&th.ResourceID,
		scanTime(&th.CreatedAt),
		scanTime(&th.UpdatedAt),
		scanJSON(&th.Metadata),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to get thread: %w", err)
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

	var th storage.Thread
	th.ID = id
	query := "SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1"
	err = tx.QueryRowContext(ctx, query, id).Scan(
		&th.Title,
		&th.ResourceID,
		scanTime(&th.CreatedAt),
		scanTime(&th.UpdatedAt),
		scanJSON(&th.Metadata),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to query thread for update: %w", err)
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

	metadataBytes := []byte("{}")
	if th.Metadata != nil {
		b, err := json.Marshal(th.Metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to marshal metadata: %w", storage.ErrInvalidInput, err)
		}
		metadataBytes = b
	}

	updateQuery := "UPDATE threads SET title = $1, resource_id = $2, updated_at = $3, metadata = $4::jsonb WHERE id = $5"
	_, err = tx.ExecContext(ctx, updateQuery, th.Title, th.ResourceID, th.UpdatedAt, string(metadataBytes), id)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to update thread: %w", err)
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

	res, err := tx.ExecContext(ctx, "DELETE FROM threads WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("postgres: failed to delete thread: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return storage.ErrThreadNotFound
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE thread_id = $1", id); err != nil {
		return fmt.Errorf("postgres: failed to cascade delete messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM thread_state WHERE thread_id = $1", id); err != nil {
		return fmt.Errorf("postgres: failed to cascade delete thread_state: %w", err)
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
	argIndex := 1

	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("resource_id = $%d", argIndex))
		args = append(args, filter.ResourceID)
		argIndex++
	}

	if len(filter.Metadata) > 0 {
		keys := make([]string, 0, len(filter.Metadata))
		for k := range filter.Metadata {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		for _, k := range keys {
			v := filter.Metadata[k]
			if v == nil {
				whereClauses = append(whereClauses, fmt.Sprintf(
					"(metadata != '{}'::jsonb AND (metadata->$%d IS NULL OR metadata->$%d = 'null'::jsonb))",
					argIndex, argIndex,
				))
				args = append(args, k)
				argIndex++
			} else {
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("%w: failed to marshal metadata filter: %w", storage.ErrInvalidInput, err)
				}
				whereClauses = append(whereClauses, fmt.Sprintf(
					"metadata->$%d = $%d::jsonb",
					argIndex, argIndex+1,
				))
				args = append(args, k, string(jsonBytes))
				argIndex += 2
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
		return nil, fmt.Errorf("postgres: failed to list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]*storage.Thread, 0)
	for rows.Next() {
		var th storage.Thread
		if err := rows.Scan(
			&th.ID,
			&th.Title,
			&th.ResourceID,
			scanTime(&th.CreatedAt),
			scanTime(&th.UpdatedAt),
			scanJSON(&th.Metadata),
		); err != nil {
			return nil, fmt.Errorf("postgres: failed to scan thread: %w", err)
		}
		results = append(results, th.Clone())
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: thread iteration error: %w", err)
	}

	return results, nil
}

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
		INSERT INTO messages (id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10::jsonb, $11::jsonb)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = EXCLUDED.thread_id,
			resource_id = EXCLUDED.resource_id,
			role = EXCLUDED.role,
			content = EXCLUDED.content,
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

	query := "SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages"
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

		query := fmt.Sprintf("SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN (%s)", strings.Join(placeholders, ", "))

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

	var val any
	query := "SELECT value FROM thread_state WHERE thread_id = $1 AND type = $2"
	err := s.db.QueryRowContext(ctx, query, threadID, stateType).Scan(scanJSON(&val))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to get thread state: %w", err)
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
		VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT(thread_id, type) DO UPDATE SET
			value = EXCLUDED.value,
			updated_at = EXCLUDED.updated_at
	`
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, query, threadID, stateType, string(b), now)
	if err != nil {
		return fmt.Errorf("postgres: failed to set thread state: %w", err)
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

	query := "DELETE FROM thread_state WHERE thread_id = $1 AND type = $2"
	if _, err := s.db.ExecContext(ctx, query, threadID, stateType); err != nil {
		return fmt.Errorf("postgres: failed to delete thread state: %w", err)
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

	query := "SELECT type, value FROM thread_state WHERE thread_id = $1"
	rows, err := s.db.QueryContext(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to list thread states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	res := make(map[string]any)
	for rows.Next() {
		var (
			stType string
			val    any
		)
		if err := rows.Scan(&stType, scanJSON(&val)); err != nil {
			return nil, fmt.Errorf("postgres: scan error in thread states: %w", err)
		}
		res[stType] = val
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iteration error in thread states: %w", err)
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
