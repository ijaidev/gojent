// Package sqlite provides a durable session.Store backed by SQLite.
//
// The store is deliberately small: it persists whole messages as JSON and
// exposes Load and atomic Append. Conversation metadata, search, and
// migrations beyond the single schema below belong outside the core.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/session"

	_ "github.com/mattn/go-sqlite3" // Register the sqlite3 driver.
)

// DefaultDriverName is the SQL driver registered by go-sqlite3.
const DefaultDriverName = "sqlite3"

// Option configures a Store.
type Option func(*options)

type options struct {
	driverName      string
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	busyTimeout     time.Duration
	autoMigrate     bool
}

// WithDriverName configures a custom registered SQL driver.
func WithDriverName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.driverName = name
		}
	}
}

// WithMaxOpenConns sets the maximum number of open connections.
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// WithMaxIdleConns sets the maximum number of idle connections.
func WithMaxIdleConns(n int) Option {
	return func(o *options) { o.maxIdleConns = n }
}

// WithConnMaxLifetime bounds connection reuse.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(o *options) { o.connMaxLifetime = d }
}

// WithBusyTimeout configures SQLite's busy timeout. Zero uses five seconds.
func WithBusyTimeout(d time.Duration) Option {
	return func(o *options) { o.busyTimeout = d }
}

// WithAutoMigrate controls whether New applies the schema. It defaults to true.
func WithAutoMigrate(enabled bool) Option {
	return func(o *options) { o.autoMigrate = enabled }
}

// Store implements session.Store using SQLite.
type Store struct {
	mu     sync.RWMutex
	closed bool
	db     *sql.DB
}

// New opens a SQLite database and applies the schema when configured. An empty
// DSN uses an in-memory database.
func New(dsn string, opts ...Option) (*Store, error) {
	if dsn == "" {
		dsn = ":memory:"
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(cfg)
	}

	isMemory := dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
	busyTimeoutMS := int(cfg.busyTimeout.Milliseconds())
	if busyTimeoutMS <= 0 {
		busyTimeoutMS = 5000
	}
	if cfg.driverName == DefaultDriverName {
		dsn = withPragmas(dsn, busyTimeoutMS, !isMemory)
	}

	db, err := sql.Open(cfg.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}

	if isMemory && cfg.maxOpenConns == 0 {
		// A pooled :memory: database would give each connection its own state.
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	store := &Store{db: db}
	if cfg.autoMigrate {
		if err := store.Migrate(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite: migrate: %w", err)
		}
	}
	return store, nil
}

// NewWithDB wraps an existing database handle.
func NewWithDB(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("sqlite: db is nil")
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(cfg)
	}
	store := &Store{db: db}
	if cfg.autoMigrate {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.Migrate(ctx); err != nil {
			return nil, fmt.Errorf("sqlite: migrate: %w", err)
		}
	}
	return store, nil
}

func defaultOptions() *options {
	return &options{
		driverName:  DefaultDriverName,
		busyTimeout: 5 * time.Second,
		autoMigrate: true,
	}
}

func withPragmas(dsn string, busyTimeoutMS int, wal bool) string {
	params := fmt.Sprintf("_busy_timeout=%d&_foreign_keys=on&_synchronous=NORMAL", busyTimeoutMS)
	if wal {
		params += "&_journal_mode=WAL"
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + params
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database. It is safe to call more than once.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

func (s *Store) available() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return session.ErrClosed
	}
	return nil
}
