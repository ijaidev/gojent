// Package storagetest provides a shared conformance suite for
// storage.Storage implementations. Backends run it to prove they honor the
// same persistence contract for threads, messages, and lifecycle behavior.
package storagetest

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/storage"
)

// Factory returns a fresh, empty store for one subtest. Implementations
// should register cleanup with t.Cleanup.
type Factory func(t *testing.T) storage.Storage

// Run executes the conformance suite against the backend produced by
// newStore. Every subtest receives its own store, so tests are isolated from
// each other.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("ThreadLifecycle", func(t *testing.T) { testThreadLifecycle(t, newStore(t)) })
	t.Run("ThreadListFilters", func(t *testing.T) { testThreadListFilters(t, newStore(t)) })
	t.Run("MessageLifecycle", func(t *testing.T) { testMessageLifecycle(t, newStore(t)) })
	t.Run("MessageListFilters", func(t *testing.T) { testMessageListFilters(t, newStore(t)) })
	t.Run("MessageUpsert", func(t *testing.T) { testMessageUpsert(t, newStore(t)) })
	t.Run("MultipartMessage", func(t *testing.T) { testMultipartMessage(t, newStore(t)) })
	t.Run("CascadeDelete", func(t *testing.T) { testCascadeDelete(t, newStore(t)) })
	t.Run("InvalidInput", func(t *testing.T) { testInvalidInput(t, newStore(t)) })
	t.Run("ClosedStore", func(t *testing.T) { testClosedStore(t, newStore(t)) })
	t.Run("ContextCancellation", func(t *testing.T) { testContextCancellation(t, newStore(t)) })
}

// saveThread persists th and fails the test on error.
func saveThread(t *testing.T, store storage.Storage, th *storage.Thread) *storage.Thread {
	t.Helper()

	saved, err := store.SaveThread(context.Background(), th)
	if err != nil {
		t.Fatalf("SaveThread(%q) failed: %v", th.Title, err)
	}
	return saved
}

// saveMessage persists msg and fails the test on error.
func saveMessage(t *testing.T, store storage.Storage, msg *storage.Message) *storage.Message {
	t.Helper()

	saved, err := store.SaveMessages(context.Background(), []*storage.Message{msg})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("expected 1 saved message, got %d", len(saved))
	}
	return saved[0]
}
