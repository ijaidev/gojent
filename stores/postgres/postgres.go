// Package postgres provides a thread-safe PostgreSQL implementation of the Gojent storage interfaces.
// It supports automatic migrations, transactions, connection pooling, and JSONB persistence.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/storage"
	_ "github.com/lib/pq" // Register postgres driver for database/sql.
)

// DefaultDriverName is the default SQL driver registered for PostgreSQL.
const DefaultDriverName = "postgres"

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
