package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/session/sessiontest"
	"github.com/ijaidev/gojent/stores/sqlite"
)

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store {
		store, err := sqlite.New(filepath.Join(t.TempDir(), "conformance.db"))
		if err != nil {
			t.Fatalf("sqlite.New: %v", err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
		return store
	})
}
