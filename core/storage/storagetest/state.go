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
	if err := store.SetThreadState(ctx, th.ID, "counters", map[string]any{"count": 1}); err != nil {
		t.Fatalf("SetThreadState counters failed: %v", err)
	}
	if err := store.SetThreadState(ctx, th.ID, "empty", ""); err != nil {
		t.Fatalf("SetThreadState empty string failed: %v", err)
	}

	got, err := store.GetThreadState(ctx, th.ID, "task")
	if err != nil {
		t.Fatalf("GetThreadState failed: %v", err)
	}
	if !reflect.DeepEqual(got, value) {
		t.Errorf("expected state %+v, got %+v", value, got)
	}

	// State values are stored as JSON, so numbers decode as float64 and
	// returned values must not share mutable state with the backend.
	counters, err := store.GetThreadState(ctx, th.ID, "counters")
	if err != nil {
		t.Fatalf("GetThreadState counters failed: %v", err)
	}
	counterMap, ok := counters.(map[string]any)
	if !ok || counterMap["count"] != float64(1) {
		t.Fatalf("expected JSON-decoded counters, got %+v (%T)", counters, counters)
	}
	counterMap["count"] = float64(2)
	againCounters, err := store.GetThreadState(ctx, th.ID, "counters")
	if err != nil {
		t.Fatalf("GetThreadState counters after mutation failed: %v", err)
	}
	if againCounters.(map[string]any)["count"] != float64(1) {
		t.Error("store returned a reference to mutable state")
	}

	empty, err := store.GetThreadState(ctx, th.ID, "empty")
	if err != nil || empty != "" {
		t.Errorf("expected empty string state, got %+v (err: %v)", empty, err)
	}

	states, err := store.ListThreadStates(ctx, th.ID)
	if err != nil {
		t.Fatalf("ListThreadStates failed: %v", err)
	}
	if len(states) != 4 || states["goal"] != "done" || states["empty"] != "" {
		t.Errorf("unexpected states: %+v", states)
	}
	states["counters"].(map[string]any)["count"] = float64(3)
	againStates, err := store.ListThreadStates(ctx, th.ID)
	if err != nil {
		t.Fatalf("ListThreadStates after mutation failed: %v", err)
	}
	if againStates["counters"].(map[string]any)["count"] != float64(1) {
		t.Error("ListThreadStates returned a reference to mutable state")
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
