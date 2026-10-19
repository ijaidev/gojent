package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/storage/storagetest"
	"github.com/ijaidev/gojent/stores/postgres"
)

// TestStore_Integration runs the shared storage conformance suite against a
// real PostgreSQL server. Set GOJENT_TEST_POSTGRES_DSN (for example
// "postgres://postgres:postgres@localhost:5432/gojent_test?sslmode=disable")
// to enable it; the test is skipped when the variable is empty.
func TestStore_Integration(t *testing.T) {
	dsn := os.Getenv("GOJENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GOJENT_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}

	storagetest.Run(t, func(t *testing.T) storage.Storage {
		t.Helper()

		store := newIntegrationStore(t, dsn)
		return store
	})
}

// newIntegrationStore resets the storage tables and returns a migrated store,
// so every conformance subtest starts from an empty database.
func newIntegrationStore(t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	_, dropErr := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS messages, thread_state, threads CASCADE")
	if closeErr := db.Close(); dropErr != nil || closeErr != nil {
		t.Fatalf("failed to reset database: drop err=%v, close err=%v", dropErr, closeErr)
	}

	store, err := postgres.New(dsn)
	if err != nil {
		t.Fatalf("postgres.New failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
