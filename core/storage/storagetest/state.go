package storagetest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func testThreadStateLifecycle(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "State"})

	if _, err := store.GetThreadState(ctx, th.ID, "task"); !errors.Is(err, storage.ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound, got %v", err)
	}

	value := map[string]any{"objective": "ship it"}
	if err := store.SetThreadState(ctx, th.ID, "task", value); err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}
	if err := store.SetThreadState(ctx, th.ID, "goal", "done"); err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	got, err := store.GetThreadState(ctx, th.ID, "task")
	if err != nil {
		t.Fatalf("GetThreadState failed: %v", err)
	}
	if !reflect.DeepEqual(got, value) {
		t.Errorf("expected state %+v, got %+v", value, got)
	}

	states, err := store.ListThreadStates(ctx, th.ID)
	if err != nil {
		t.Fatalf("ListThreadStates failed: %v", err)
	}
	if len(states) != 2 || states["goal"] != "done" {
		t.Errorf("unexpected states: %+v", states)
	}

	if err := store.DeleteThreadState(ctx, th.ID, "task"); err != nil {
		t.Fatalf("DeleteThreadState failed: %v", err)
	}
	if _, err := store.GetThreadState(ctx, th.ID, "task"); !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after delete, got %v", err)
	}
}

func testCascadeDelete(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Cascade"})

	if _, err := store.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "one"),
		storage.NewMessage(th.ID, llm.RoleAssistant, "two"),
	}); err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if err := store.SetThreadState(ctx, th.ID, "task", "pending"); err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
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
	if _, err := store.GetThreadState(ctx, th.ID, "task"); !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after cascade, got %v", err)
	}
}
