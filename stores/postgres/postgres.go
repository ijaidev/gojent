// Package postgres provides a durable session.Store backed by PostgreSQL.
//
// The store persists whole messages as JSON and exposes only Load and atomic
// Append. Conversation metadata, search, and orchestration state belong in
// higher-level modules.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/session"

	_ "github.com/lib/pq" // Register the postgres driver.
)

// DefaultDriverName is the SQL driver registered by lib/pq.
const DefaultDriverName = "postgres"

// Option configures a Store.
type Option func(*options)

type options struct {
	driverName      string
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
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

// WithAutoMigrate controls whether New applies the schema. It defaults to true.
func WithAutoMigrate(enabled bool) Option {
	return func(o *options) { o.autoMigrate = enabled }
}

// Store implements session.Store using PostgreSQL.
type Store struct {
	mu     sync.RWMutex
	closed bool
	db     *sql.DB
}

// New opens a PostgreSQL database and applies the schema when configured.
func New(dsn string, opts ...Option) (*Store, error) {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(cfg)
	}

	db, err := sql.Open(cfg.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	store, err := NewWithDB(db, opts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewWithDB wraps an existing database handle.
func NewWithDB(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres: db is nil")
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
			return nil, fmt.Errorf("postgres: migrate: %w", err)
		}
	}
	return store, nil
}

func defaultOptions() *options {
	return &options{
		driverName:  DefaultDriverName,
		autoMigrate: true,
	}
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
