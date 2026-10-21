package storagetest

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func testCascadeDelete(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Cascade"})

	if _, err := store.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "one"),
		storage.NewMessage(th.ID, llm.RoleAssistant, "two"),
	}); err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	if err := store.DeleteThread(ctx, th.ID); err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cascade, got %d", len(msgs))
	}
}
