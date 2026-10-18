package inmemory_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/stores/inmemory"
)

func TestStore_ThreadLifecycle(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	// 1. Save thread with auto-generated ID
	th1, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "Initial Thread",
		ResourceID: "res-1",
		Metadata: map[string]any{
			"env": "production",
		},
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if th1.ID == "" {
		t.Fatalf("expected generated ID")
	}
	if th1.CreatedAt.IsZero() || th1.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps")
	}

	// 2. GetThreadByID
	fetched, err := store.GetThreadByID(ctx, th1.ID)
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if fetched.Title != th1.Title || fetched.ResourceID != th1.ResourceID {
		t.Errorf("fetched thread mismatch: %+v vs %+v", fetched, th1)
	}

	// 3. UpdateThread
	newTitle := "Renamed Thread"
	updated, err := store.UpdateThread(ctx, th1.ID, storage.UpdateThreadInput{
		Title: &newTitle,
		Metadata: map[string]any{
			"status": "archived",
		},
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected updated title %q, got %q", newTitle, updated.Title)
	}
	if updated.Metadata["env"] != "production" || updated.Metadata["status"] != "archived" {
		t.Errorf("metadata not merged properly: %+v", updated.Metadata)
	}

	// 4. ListThreads with filters, pagination, and sorting
	_, err = store.SaveThread(ctx, &storage.Thread{
		ID:         "th-2",
		Title:      "Second Thread",
		ResourceID: "res-2",
		Metadata: map[string]any{
			"env": "staging",
		},
	})
	if err != nil {
		t.Fatalf("SaveThread th-2 failed: %v", err)
	}

	// Filter by ResourceID
	res1Threads, err := store.ListThreads(ctx, storage.ThreadFilter{ResourceID: "res-1"})
	if err != nil {
		t.Fatalf("ListThreads by ResourceID failed: %v", err)
	}
	if len(res1Threads) != 1 || res1Threads[0].ID != th1.ID {
		t.Errorf("expected 1 thread for res-1, got %d", len(res1Threads))
	}

	// Sort Descending
	descThreads, err := store.ListThreads(ctx, storage.ThreadFilter{SortOrder: storage.SortDesc})
	if err != nil {
		t.Fatalf("ListThreads SortDesc failed: %v", err)
	}
	if len(descThreads) != 2 || descThreads[0].ID != "th-2" {
		t.Errorf("expected newest thread th-2 first, got %v", descThreads[0].ID)
	}

	// 5. DeleteThread
	err = store.DeleteThread(ctx, th1.ID)
	if err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}
	_, err = store.GetThreadByID(ctx, th1.ID)
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestStore_MessageOperations(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "Message Thread",
		ResourceID: "res-user",
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Save messages
	t0 := time.Now().UTC().Add(-10 * time.Minute)
	t1 := time.Now().UTC()

	m1 := &storage.Message{
		ThreadID:  th.ID,
		Role:      llm.RoleUser,
		Content:   []llm.Part{llm.TextPart("Initial question")},
		CreatedAt: t0,
	}
	m2 := &storage.Message{
		ThreadID:  th.ID,
		Role:      llm.RoleAssistant,
		Content:   []llm.Part{llm.TextPart("Here is an answer")},
		CreatedAt: t1,
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "calculator", Arguments: `{"expr":"2+2"}`},
		},
	}

	saved, err := store.SaveMessages(ctx, []*storage.Message{m1, m2})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved messages, got %d", len(saved))
	}
	if saved[0].ResourceID != "res-user" {
		t.Errorf("expected inherited resourceID 'res-user', got %q", saved[0].ResourceID)
	}

	// ListMessages filtering by Role
	userMsgs, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID: th.ID,
		Role:     llm.RoleUser,
	})
	if err != nil {
		t.Fatalf("ListMessages by Role failed: %v", err)
	}
	if len(userMsgs) != 1 || userMsgs[0].Text() != "Initial question" {
		t.Errorf("unexpected user message filter result: %+v", userMsgs)
	}

	// ListMessages date range
	since := t0.Add(1 * time.Minute)
	afterSinceMsgs, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID: th.ID,
		Since:    &since,
	})
	if err != nil {
		t.Fatalf("ListMessages Since failed: %v", err)
	}
	if len(afterSinceMsgs) != 1 || afterSinceMsgs[0].Text() != "Here is an answer" {
		t.Errorf("unexpected since filter result: %+v", afterSinceMsgs)
	}

	// GetMessagesByID
	byIDs, err := store.GetMessagesByID(ctx, []string{saved[1].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byIDs) != 1 || byIDs[0].ID != saved[1].ID {
		t.Errorf("GetMessagesByID failed to find message: %+v", byIDs)
	}

	// DeleteMessages
	err = store.DeleteMessages(ctx, []string{saved[0].ID})
	if err != nil {
		t.Fatalf("DeleteMessages failed: %v", err)
	}
	remaining, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != saved[1].ID {
		t.Errorf("expected remaining message, got %+v", remaining)
	}
}

func TestStore_ThreadState(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	threadID := "th-state-test"

	// Set state for "task"
	taskData := map[string]any{"task_id": "1", "status": "running"}
	err := store.SetThreadState(ctx, threadID, "task", taskData)
	if err != nil {
		t.Fatalf("SetThreadState task failed: %v", err)
	}

	// Set state for "goal"
	goalData := "Achieve 100% test coverage"
	err = store.SetThreadState(ctx, threadID, "goal", goalData)
	if err != nil {
		t.Fatalf("SetThreadState goal failed: %v", err)
	}

	// Get state
	gotTask, err := store.GetThreadState(ctx, threadID, "task")
	if err != nil {
		t.Fatalf("GetThreadState failed: %v", err)
	}
	if m, ok := gotTask.(map[string]any); !ok || m["task_id"] != "1" {
		t.Errorf("unexpected task state: %+v", gotTask)
	}

	// List states
	states, err := store.ListThreadStates(ctx, threadID)
	if err != nil {
		t.Fatalf("ListThreadStates failed: %v", err)
	}
	if len(states) != 2 || states["goal"] != goalData {
		t.Errorf("unexpected state list: %+v", states)
	}

	// Delete state
	err = store.DeleteThreadState(ctx, threadID, "task")
	if err != nil {
		t.Fatalf("DeleteThreadState failed: %v", err)
	}
	_, err = store.GetThreadState(ctx, threadID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after delete, got %v", err)
	}
}

func TestStore_CascadeDelete(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Cascade Parent"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	_, err = store.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "msg 1"),
		storage.NewMessage(th.ID, llm.RoleAssistant, "msg 2"),
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	err = store.SetThreadState(ctx, th.ID, "task", "important task")
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// Delete thread
	err = store.DeleteThread(ctx, th.ID)
	if err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	// Check messages cascaded
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cascade, got %d", len(msgs))
	}

	// Check thread state cascaded
	_, err = store.GetThreadState(ctx, th.ID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after cascade, got %v", err)
	}
}

func TestStore_ClearAndDangerouslyClearAll(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, _ := store.SaveThread(ctx, &storage.Thread{Title: "Thread to clear"})
	_, _ = store.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "msg"),
	})
	_ = store.SetThreadState(ctx, th.ID, "k", "v")

	store.DangerouslyClearAll()

	threads, _ := store.ListThreads(ctx, storage.ThreadFilter{})
	if len(threads) != 0 {
		t.Errorf("expected 0 threads after clear, got %d", len(threads))
	}
	msgs, _ := store.ListMessages(ctx, storage.MessageFilter{})
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(msgs))
	}
	states, _ := store.ListThreadStates(ctx, th.ID)
	if len(states) != 0 {
		t.Errorf("expected 0 states after clear, got %d", len(states))
	}
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

				// Set and get state
				stateKey := fmt.Sprintf("state-%d", workerID)
				err = store.SetThreadState(ctx, th.ID, stateKey, j)
				if err != nil {
					t.Errorf("SetThreadState concurrent error: %v", err)
				}

				_, err = store.GetThreadState(ctx, th.ID, stateKey)
				if err != nil {
					t.Errorf("GetThreadState concurrent error: %v", err)
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

func TestStore_ClosedAndInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()

	// Invalid input tests
	if _, err := store.SaveThread(ctx, nil); err == nil {
		t.Errorf("expected error for nil thread")
	}
	if _, err := store.GetThreadByID(ctx, ""); !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired for empty ID, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{nil}); err == nil {
		t.Errorf("expected error for nil message")
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{{Role: llm.RoleUser}}); err == nil {
		t.Errorf("expected error for message without threadID")
	}

	// Close store
	err := store.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Operations should return ErrClosed
	if _, err := store.SaveThread(ctx, &storage.Thread{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SaveThread, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, "id"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for GetThreadByID, got %v", err)
	}
	if err := store.DeleteThread(ctx, "id"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for DeleteThread, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SaveMessages, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for ListMessages, got %v", err)
	}
	if err := store.SetThreadState(ctx, "th", "type", "val"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SetThreadState, got %v", err)
	}
}

func TestStore_MessageUpdateNoDuplicatesAndTimestampPreservation(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Test Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// 1. Initial message save
	saved, err := store.SaveMessages(ctx, []*storage.Message{
		{ID: "msg-1", ThreadID: th.ID, Role: llm.RoleUser, Content: []llm.Part{llm.TextPart("Version 1")}},
	})
	if err != nil {
		t.Fatalf("SaveMessages v1 failed: %v", err)
	}
	origCreatedAt := saved[0].CreatedAt

	// 2. Update the message by saving with same ID and empty CreatedAt
	updated, err := store.SaveMessages(ctx, []*storage.Message{
		{ID: "msg-1", ThreadID: th.ID, Role: llm.RoleUser, Content: []llm.Part{llm.TextPart("Version 2")}},
	})
	if err != nil {
		t.Fatalf("SaveMessages v2 failed: %v", err)
	}
	if updated[0].Text() != "Version 2" {
		t.Errorf("expected content 'Version 2', got %q", updated[0].Content)
	}
	if !updated[0].CreatedAt.Equal(origCreatedAt) {
		t.Errorf("expected preserved CreatedAt timestamp %v, got %v", origCreatedAt, updated[0].CreatedAt)
	}

	// 3. List messages must NOT contain duplicate entries
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 message after update, got %d", len(msgs))
	}
	if msgs[0].Text() != "Version 2" {
		t.Errorf("expected 'Version 2', got %q", msgs[0].Content)
	}
}

func TestStore_UpdateThread_ResourceID(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Thread Without Resource"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if th.ResourceID != "" {
		t.Errorf("expected empty resourceID")
	}

	resID := "tenant-99"
	updated, err := store.UpdateThread(ctx, th.ID, storage.UpdateThreadInput{
		ResourceID: &resID,
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if updated.ResourceID != "tenant-99" {
		t.Errorf("expected updated resourceID 'tenant-99', got %q", updated.ResourceID)
	}
}
