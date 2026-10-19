// Package sqlite provides a thread-safe, ACID-compliant SQLite implementation of the Gojent storage interfaces.
// It supports in-memory databases, file-based databases, automatic migrations, transactions, and concurrent access.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/storage"
	_ "github.com/mattn/go-sqlite3" // Register sqlite3 driver for database/sql.
)

// DefaultDriverName is the default SQL driver registered for SQLite.
const DefaultDriverName = "sqlite3"

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

	// For in-memory databases, restrict connection pool to 1 by default so that
	// separate queries share the exact same in-memory state.
	isMemory := dsn == ":memory:" || strings.Contains(dsn, "mode=memory")

	busyTimeoutMs := int(cfg.busyTimeout.Milliseconds())
	if busyTimeoutMs <= 0 {
		busyTimeoutMs = 5000
	}

	// database/sql pools connections, so PRAGMAs executed once after opening
	// only configure whichever connection the pool happened to hand out.
	// Encode them in the DSN so every connection is configured identically.
	if cfg.driverName == DefaultDriverName {
		dsn = dsnWithPragmas(dsn, busyTimeoutMs, !isMemory)
	}

	db, err := sql.Open(cfg.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to open database: %w", err)
	}
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

	// Custom drivers may not understand the DSN pragma parameters above;
	// configure the pooled connection as a best-effort fallback.
	if cfg.driverName != DefaultDriverName {
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

// dsnWithPragmas appends go-sqlite3 DSN parameters so that database/sql opens
// every pooled connection with the pragmas already applied.
func dsnWithPragmas(dsn string, busyTimeoutMs int, wal bool) string {
	params := fmt.Sprintf("_busy_timeout=%d&_foreign_keys=on&_synchronous=NORMAL", busyTimeoutMs)
	if wal {
		params += "&_journal_mode=WAL"
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + params
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

// Compile-time checks that Store implements storage.Storage and all domain store interfaces.
var (
	_ storage.Storage          = (*Store)(nil)
	_ storage.MemoryStore      = (*Store)(nil)
	_ storage.ThreadStore      = (*Store)(nil)
	_ storage.MessageStore     = (*Store)(nil)
	_ storage.ThreadStateStore = (*Store)(nil)
)
