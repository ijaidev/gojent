package inmemory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/storage/storagetest"
	"github.com/ijaidev/gojent/stores/inmemory"
)

// TestStore_Conformance runs the shared storage contract against the
// in-memory backend.
func TestStore_Conformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Storage {
		store := inmemory.New()
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}

func TestStore_ClearAndDangerouslyClearAll(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	seed := func() {
		th, err := store.SaveThread(ctx, &storage.Thread{Title: "Thread to clear"})
		if err != nil {
			t.Fatalf("SaveThread failed: %v", err)
		}
		if _, err := store.SaveMessages(ctx, []*storage.Message{
			storage.NewMessage(th.ID, llm.RoleUser, "msg"),
		}); err != nil {
			t.Fatalf("SaveMessages failed: %v", err)
		}
	}
	assertEmpty := func() {
		t.Helper()
		threads, _ := store.ListThreads(ctx, storage.ThreadFilter{})
		if len(threads) != 0 {
			t.Errorf("expected 0 threads after clear, got %d", len(threads))
		}
		msgs, _ := store.ListMessages(ctx, storage.MessageFilter{})
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages after clear, got %d", len(msgs))
		}
	}

	seed()
	store.Clear()
	assertEmpty()

	seed()
	store.DangerouslyClearAll()
	assertEmpty()
}

func TestStore_Concurrency(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Concurrent Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	var wg sync.WaitGroup
	workers := 30
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Write message
				msg := storage.NewMessage(th.ID, llm.RoleUser, fmt.Sprintf("worker %d iter %d", workerID, j))
				_, err := store.SaveMessages(ctx, []*storage.Message{msg})
				if err != nil {
					t.Errorf("SaveMessages concurrent error: %v", err)
				}

				// Read messages
				_, err = store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID, Limit: 5})
				if err != nil {
					t.Errorf("ListMessages concurrent error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	totalMsgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages after concurrency failed: %v", err)
	}
	expectedTotal := workers * iterations
	if len(totalMsgs) != expectedTotal {
		t.Errorf("expected %d messages total, got %d", expectedTotal, len(totalMsgs))
	}
}
