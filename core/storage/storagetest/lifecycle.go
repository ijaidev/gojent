package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func testInvalidInput(t *testing.T, store storage.Storage) {
	ctx := context.Background()

	if _, err := store.SaveThread(ctx, nil); !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil thread, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, ""); !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired for empty thread ID, got %v", err)
	}
	if _, err := store.UpdateThread(ctx, "", storage.UpdateThreadInput{}); !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired updating empty ID, got %v", err)
	}
	if err := store.DeleteThread(ctx, ""); !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired deleting empty ID, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{nil}); !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil message, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{{Role: llm.RoleUser}}); !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired for message without thread, got %v", err)
	}
}

func testClosedStore(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := store.SaveThread(ctx, &storage.Thread{Title: "closed"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed from SaveThread, got %v", err)
	}
	if _, err := store.ListThreads(ctx, storage.ThreadFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed from ListThreads, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed from ListMessages, got %v", err)
	}
}

func testContextCancellation(t *testing.T, store storage.Storage) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.SaveThread(ctx, &storage.Thread{Title: "canceled"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from SaveThread, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from ListMessages, got %v", err)
	}
}
