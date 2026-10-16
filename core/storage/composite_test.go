package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func TestCompositeStore_Routing(t *testing.T) {
	ctx := context.Background()

	defaultBackend := storage.NewMockStorage()
	dedicatedMessageBackend := storage.NewMockStorage()
	dedicatedStateBackend := storage.NewMockStorage()

	composite, err := storage.NewComposite(
		storage.WithDefaultStore(defaultBackend),
		storage.WithMessageStore(dedicatedMessageBackend),
		storage.WithThreadStateStore(dedicatedStateBackend),
	)
	if err != nil {
		t.Fatalf("NewComposite failed: %v", err)
	}
	defer func() { _ = composite.Close() }()

	// 1. Thread operations route to defaultBackend
	th, err := composite.SaveThread(ctx, &storage.Thread{
		Title: "Composite Thread",
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Verify thread is in defaultBackend, not in messageBackend
	if _, err := defaultBackend.GetThreadByID(ctx, th.ID); err != nil {
		t.Errorf("expected thread in defaultBackend: %v", err)
	}
	if _, err := dedicatedMessageBackend.GetThreadByID(ctx, th.ID); err == nil {
		t.Errorf("thread unexpectedly found in dedicatedMessageBackend")
	}

	// 2. Message operations route to dedicatedMessageBackend
	msgs, err := composite.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "composite message"),
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	// Verify message is in dedicatedMessageBackend, not in defaultBackend
	foundInDedicated, err := dedicatedMessageBackend.GetMessagesByID(ctx, []string{msgs[0].ID})
	if err != nil || len(foundInDedicated) != 1 {
		t.Errorf("expected message in dedicatedMessageBackend")
	}
	foundInDefault, _ := defaultBackend.GetMessagesByID(ctx, []string{msgs[0].ID})
	if len(foundInDefault) != 0 {
		t.Errorf("message unexpectedly found in defaultBackend")
	}

	// 3. ThreadState operations route to dedicatedStateBackend
	err = composite.SetThreadState(ctx, th.ID, "task", "run-tests")
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	stateVal, err := dedicatedStateBackend.GetThreadState(ctx, th.ID, "task")
	if err != nil || stateVal != "run-tests" {
		t.Errorf("expected state in dedicatedStateBackend")
	}
	_, err = defaultBackend.GetThreadState(ctx, th.ID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("state unexpectedly found in defaultBackend")
	}
}

func TestCompositeStore_Validation(t *testing.T) {
	// Attempt creating CompositeStore without any store
	_, err := storage.NewComposite()
	if err == nil {
		t.Fatalf("expected error creating empty composite store")
	}
}

func TestCompositeStore_CascadeDeleteAcrossBackends(t *testing.T) {
	ctx := context.Background()

	defaultBackend := storage.NewMockStorage()
	dedicatedMessageBackend := storage.NewMockStorage()
	dedicatedStateBackend := storage.NewMockStorage()

	composite, err := storage.NewComposite(
		storage.WithDefaultStore(defaultBackend),
		storage.WithMessageStore(dedicatedMessageBackend),
		storage.WithThreadStateStore(dedicatedStateBackend),
	)
	if err != nil {
		t.Fatalf("NewComposite failed: %v", err)
	}
	defer func() { _ = composite.Close() }()

	th, err := composite.SaveThread(ctx, &storage.Thread{Title: "Cascade Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	msgs, err := composite.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "msg1"),
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	err = composite.SetThreadState(ctx, th.ID, "goal", "finish-project")
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// Delete thread via composite
	err = composite.DeleteThread(ctx, th.ID)
	if err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	// Thread should be gone from defaultBackend
	_, err = defaultBackend.GetThreadByID(ctx, th.ID)
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected thread to be deleted in defaultBackend, got %v", err)
	}

	// Messages should be cascaded and deleted from dedicatedMessageBackend
	foundMsgs, err := dedicatedMessageBackend.GetMessagesByID(ctx, []string{msgs[0].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(foundMsgs) != 0 {
		t.Errorf("expected messages to be cascaded and deleted from dedicatedMessageBackend, found %d", len(foundMsgs))
	}

	// States should be cascaded and deleted from dedicatedStateBackend
	_, err = dedicatedStateBackend.GetThreadState(ctx, th.ID, "goal")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected thread state to be cascaded and deleted from dedicatedStateBackend, got %v", err)
	}
}

func TestCompositeStore_Closed(t *testing.T) {
	ctx := context.Background()
	backend := storage.NewMockStorage()

	composite, err := storage.NewComposite(storage.WithDefaultStore(backend))
	if err != nil {
		t.Fatalf("NewComposite failed: %v", err)
	}

	if err := composite.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Subsequent close should return ErrClosed
	if err := composite.Close(); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed on double close, got %v", err)
	}

	// Operations should return ErrClosed
	_, err = composite.SaveThread(ctx, &storage.Thread{Title: "After Close"})
	if !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SaveThread after Close, got %v", err)
	}
}
