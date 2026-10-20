package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/storage"
)

func testThreadLifecycle(t *testing.T, store storage.Storage) {
	ctx := context.Background()

	created, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "First",
		ResourceID: "user-1",
		Metadata:   map[string]any{"env": "test"},
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected an auto-generated thread ID")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("expected non-zero timestamps")
	}

	fetched, err := store.GetThreadByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if fetched.Title != "First" || fetched.ResourceID != "user-1" {
		t.Errorf("unexpected thread: %+v", fetched)
	}
	if fetched.Metadata["env"] != "test" {
		t.Errorf("metadata not persisted: %+v", fetched.Metadata)
	}

	// Callers must not be able to mutate normalized fields or top-level metadata
	// through returned values.
	fetched.Title = "mutated"
	fetched.Metadata["env"] = "mutated"
	again, err := store.GetThreadByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetThreadByID after mutation failed: %v", err)
	}
	if again.Title != "First" {
		t.Error("store returned a reference to internal state")
	}
	if again.Metadata["env"] != "test" {
		t.Error("store returned a reference to top-level metadata")
	}

	title := "Updated"
	updated, err := store.UpdateThread(ctx, created.ID, storage.UpdateThreadInput{
		Title:    &title,
		Metadata: map[string]any{"status": "active"},
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if updated.Title != "Updated" {
		t.Errorf("expected title %q, got %q", "Updated", updated.Title)
	}
	if updated.Metadata["env"] != "test" || updated.Metadata["status"] != "active" {
		t.Errorf("metadata was not merged: %+v", updated.Metadata)
	}

	if err := store.DeleteThread(ctx, created.ID); err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}
	if _, err := store.GetThreadByID(ctx, created.ID); !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound after delete, got %v", err)
	}
	if err := store.DeleteThread(ctx, created.ID); !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound deleting twice, got %v", err)
	}
}

func testThreadListFilters(t *testing.T, store storage.Storage) {
	ctx := context.Background()

	first := saveThread(t, store, &storage.Thread{
		ID:         "th-1",
		Title:      "First",
		ResourceID: "user-1",
		Metadata:   map[string]any{"env": "prod"},
	})
	time.Sleep(2 * time.Millisecond) // guarantee distinct CreatedAt values
	second := saveThread(t, store, &storage.Thread{
		ID:         "th-2",
		Title:      "Second",
		ResourceID: "user-2",
		Metadata:   map[string]any{"env": "test"},
	})

	byResource, err := store.ListThreads(ctx, storage.ThreadFilter{ResourceID: "user-1"})
	if err != nil {
		t.Fatalf("ListThreads by resource failed: %v", err)
	}
	if len(byResource) != 1 || byResource[0].ID != first.ID {
		t.Errorf("expected only %s, got %+v", first.ID, byResource)
	}

	byMetadata, err := store.ListThreads(ctx, storage.ThreadFilter{
		Metadata: map[string]any{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("ListThreads by metadata failed: %v", err)
	}
	if len(byMetadata) != 1 || byMetadata[0].ID != first.ID {
		t.Errorf("expected only %s, got %+v", first.ID, byMetadata)
	}

	asc, err := store.ListThreads(ctx, storage.ThreadFilter{SortOrder: storage.SortAsc})
	if err != nil {
		t.Fatalf("ListThreads ascending failed: %v", err)
	}
	if len(asc) != 2 || asc[0].ID != first.ID || asc[1].ID != second.ID {
		t.Errorf("unexpected ascending order: %+v", asc)
	}

	desc, err := store.ListThreads(ctx, storage.ThreadFilter{SortOrder: storage.SortDesc})
	if err != nil {
		t.Fatalf("ListThreads descending failed: %v", err)
	}
	if len(desc) != 2 || desc[0].ID != second.ID || desc[1].ID != first.ID {
		t.Errorf("unexpected descending order: %+v", desc)
	}

	page, err := store.ListThreads(ctx, storage.ThreadFilter{
		SortOrder: storage.SortAsc,
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("ListThreads pagination failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != second.ID {
		t.Errorf("expected page to contain %s, got %+v", second.ID, page)
	}
}
