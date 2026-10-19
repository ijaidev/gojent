package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/stores/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

func TestStore_ThreadLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
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
	if fetched.Metadata["env"] != "production" {
		t.Errorf("expected metadata env=production, got %v", fetched.Metadata)
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
	time.Sleep(10 * time.Millisecond) // ensure distinct timestamps
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

	// Filter by Metadata
	stagingThreads, err := store.ListThreads(ctx, storage.ThreadFilter{
		Metadata: map[string]any{"env": "staging"},
	})
	if err != nil {
		t.Fatalf("ListThreads by Metadata failed: %v", err)
	}
	if len(stagingThreads) != 1 || stagingThreads[0].ID != "th-2" {
		t.Errorf("expected 1 thread for env=staging, got %d", len(stagingThreads))
	}

	// Sort Descending
	descThreads, err := store.ListThreads(ctx, storage.ThreadFilter{SortOrder: storage.SortDesc})
	if err != nil {
		t.Fatalf("ListThreads SortDesc failed: %v", err)
	}
	if len(descThreads) != 2 || descThreads[0].ID != "th-2" {
		t.Errorf("expected newest thread th-2 first, got %v", descThreads[0].ID)
	}

	// Pagination
	pagedThreads, err := store.ListThreads(ctx, storage.ThreadFilter{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("ListThreads pagination failed: %v", err)
	}
	if len(pagedThreads) != 1 || pagedThreads[0].ID != "th-2" {
		t.Errorf("expected 1 paginated thread th-2, got %+v", pagedThreads)
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

	// Delete non-existent thread
	err = store.DeleteThread(ctx, "non-existent")
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound for non-existent thread, got %v", err)
	}
}

func TestStore_MessageOperations(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "Message Thread",
		ResourceID: "res-user",
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Save messages
	t0 := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond)
	t1 := time.Now().UTC().Truncate(time.Microsecond)

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
		Metadata: map[string]any{"source": "test"},
		Raw:      map[string]any{"raw_field": 123},
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

	// Verify thread UpdatedAt was updated
	updatedThread, err := store.GetThreadByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if updatedThread.UpdatedAt.Before(t1) {
		t.Errorf("expected thread UpdatedAt >= t1, got %v", updatedThread.UpdatedAt)
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

	// Verify tool calls and raw payloads restored
	if len(afterSinceMsgs[0].ToolCalls) != 1 || afterSinceMsgs[0].ToolCalls[0].ID != "c1" {
		t.Errorf("tool calls mismatch: %+v", afterSinceMsgs[0].ToolCalls)
	}
	if afterSinceMsgs[0].Metadata["source"] != "test" {
		t.Errorf("metadata mismatch: %+v", afterSinceMsgs[0].Metadata)
	}

	// GetMessagesByID preserves requested order
	byIDs, err := store.GetMessagesByID(ctx, []string{saved[1].ID, saved[0].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byIDs) != 2 || byIDs[0].ID != saved[1].ID || byIDs[1].ID != saved[0].ID {
		t.Errorf("GetMessagesByID order mismatch: %+v", byIDs)
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

	// Verify preservation of empty non-nil vs nil slices/maps
	msgNilMeta := &storage.Message{
		ThreadID: th.ID,
		Role:     llm.RoleUser,
		Content:  []llm.Part{llm.TextPart("Nil fields")},
	}
	msgEmptyMeta := &storage.Message{
		ThreadID:  th.ID,
		Role:      llm.RoleAssistant,
		Content:   []llm.Part{llm.TextPart("Empty non-nil fields")},
		ToolCalls: []llm.ToolCall{},
		Metadata:  map[string]any{},
	}
	savedFields, err := store.SaveMessages(ctx, []*storage.Message{msgNilMeta, msgEmptyMeta})
	if err != nil {
		t.Fatalf("SaveMessages with empty/nil fields failed: %v", err)
	}
	fetchedFields, err := store.GetMessagesByID(ctx, []string{savedFields[0].ID, savedFields[1].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID for field checks failed: %v", err)
	}
	if fetchedFields[0].Metadata != nil {
		t.Errorf("expected nil Metadata for msgNilMeta, got %+v", fetchedFields[0].Metadata)
	}
	if fetchedFields[0].ToolCalls != nil {
		t.Errorf("expected nil ToolCalls for msgNilMeta, got %+v", fetchedFields[0].ToolCalls)
	}
	if fetchedFields[1].Metadata == nil {
		t.Errorf("expected non-nil empty Metadata for msgEmptyMeta, got nil")
	}
	if fetchedFields[1].ToolCalls == nil {
		t.Errorf("expected non-nil empty ToolCalls for msgEmptyMeta, got nil")
	}
}

func TestStore_ThreadState(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	threadID := "th-state-test"

	// Set state for "task"
	taskData := map[string]any{"task_id": "1", "status": "running"}
	err = store.SetThreadState(ctx, threadID, "task", taskData)
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
	m, ok := gotTask.(map[string]any)
	if !ok || m["task_id"] != "1" {
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
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
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
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
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
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Concurrent Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	var wg sync.WaitGroup
	workers := 20
	iterations := 15

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
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}

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
	if _, err := store.GetThreadState(ctx, "", "type"); err == nil {
		t.Errorf("expected error for empty threadID in GetThreadState")
	}
	if err := store.SetThreadState(ctx, "th", "", "val"); err == nil {
		t.Errorf("expected error for empty stateType in SetThreadState")
	}
	if err := store.DeleteThreadState(ctx, "", "type"); err == nil {
		t.Errorf("expected error for empty threadID in DeleteThreadState")
	}
	if _, err := store.ListThreadStates(ctx, ""); err == nil {
		t.Errorf("expected error for empty threadID in ListThreadStates")
	}

	// Close store
	err = store.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Calling Close again should be idempotent
	if err := store.Close(); err != nil {
		t.Fatalf("Idempotent Close failed: %v", err)
	}

	// Operations should return ErrClosed
	if _, err := store.SaveThread(ctx, &storage.Thread{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SaveThread, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, "id"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for GetThreadByID, got %v", err)
	}
	if _, err := store.UpdateThread(ctx, "id", storage.UpdateThreadInput{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for UpdateThread, got %v", err)
	}
	if err := store.DeleteThread(ctx, "id"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for DeleteThread, got %v", err)
	}
	if _, err := store.ListThreads(ctx, storage.ThreadFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for ListThreads, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SaveMessages, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for ListMessages, got %v", err)
	}
	if _, err := store.GetMessagesByID(ctx, []string{"id"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for GetMessagesByID, got %v", err)
	}
	if err := store.DeleteMessages(ctx, []string{"id"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for DeleteMessages, got %v", err)
	}
	if _, err := store.GetThreadState(ctx, "th", "type"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for GetThreadState, got %v", err)
	}
	if err := store.SetThreadState(ctx, "th", "type", "val"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for SetThreadState, got %v", err)
	}
	if err := store.DeleteThreadState(ctx, "th", "type"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for DeleteThreadState, got %v", err)
	}
	if _, err := store.ListThreadStates(ctx, "th"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for ListThreadStates, got %v", err)
	}
	if err := store.Clear(ctx); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed for Clear, got %v", err)
	}
}

func TestStore_MessageUpdateNoDuplicatesAndTimestampPreservation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
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

func TestStore_FilePersistence(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_persistent.db")

	// 1. Open store on file and insert data
	store1, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("failed to open file store: %v", err)
	}

	th, err := store1.SaveThread(ctx, &storage.Thread{
		ID:    "persistent-th",
		Title: "Persistent Thread",
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	_, err = store1.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "persistent message"),
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	err = store1.SetThreadState(ctx, th.ID, "goal", "persistent goal")
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// Close store1
	if err := store1.Close(); err != nil {
		t.Fatalf("failed to close store1: %v", err)
	}

	// 2. Open store2 on same file and verify data
	store2, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("failed to open store2: %v", err)
	}
	defer func() { _ = store2.Close() }()

	fetchedTh, err := store2.GetThreadByID(ctx, "persistent-th")
	if err != nil {
		t.Fatalf("GetThreadByID in store2 failed: %v", err)
	}
	if fetchedTh.Title != "Persistent Thread" {
		t.Errorf("expected title 'Persistent Thread', got %q", fetchedTh.Title)
	}

	msgs, err := store2.ListMessages(ctx, storage.MessageFilter{ThreadID: "persistent-th"})
	if err != nil {
		t.Fatalf("ListMessages in store2 failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text() != "persistent message" {
		t.Errorf("expected 1 persistent message, got %+v", msgs)
	}

	goalVal, err := store2.GetThreadState(ctx, "persistent-th", "goal")
	if err != nil {
		t.Fatalf("GetThreadState in store2 failed: %v", err)
	}
	if goalVal != "persistent goal" {
		t.Errorf("expected 'persistent goal', got %v", goalVal)
	}
}

func TestStore_NewWithDBAndOptions(t *testing.T) {
	ctx := context.Background()

	// NewWithDB with nil db
	if _, err := sqlite.NewWithDB(nil); err == nil {
		t.Errorf("expected error for nil db")
	}

	// Custom *sql.DB
	rawDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	store, err := sqlite.NewWithDB(rawDB, sqlite.WithAutoMigrate(true))
	if err != nil {
		t.Fatalf("NewWithDB failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.DB() != rawDB {
		t.Errorf("DB() did not return original db")
	}

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "WithDB Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if th.ID == "" {
		t.Fatalf("expected generated ID")
	}
}

func TestStore_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := store.SaveThread(ctx, &storage.Thread{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for SaveThread, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for GetThreadByID, got %v", err)
	}
	if _, err := store.UpdateThread(ctx, "id", storage.UpdateThreadInput{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for UpdateThread, got %v", err)
	}
	if err := store.DeleteThread(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for DeleteThread, got %v", err)
	}
	if _, err := store.ListThreads(ctx, storage.ThreadFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for ListThreads, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for SaveMessages, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for ListMessages, got %v", err)
	}
	if _, err := store.GetMessagesByID(ctx, []string{"id"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for GetMessagesByID, got %v", err)
	}
	if err := store.DeleteMessages(ctx, []string{"id"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for DeleteMessages, got %v", err)
	}
	if _, err := store.GetThreadState(ctx, "th", "type"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for GetThreadState, got %v", err)
	}
	if err := store.SetThreadState(ctx, "th", "type", "val"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for SetThreadState, got %v", err)
	}
	if err := store.DeleteThreadState(ctx, "th", "type"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for DeleteThreadState, got %v", err)
	}
	if _, err := store.ListThreadStates(ctx, "th"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for ListThreadStates, got %v", err)
	}
	if err := store.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for Clear, got %v", err)
	}
}

func TestStore_BatchChunking(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Batch Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Create 600 messages (> 500 chunk size)
	count := 600
	msgs := make([]*storage.Message, count)
	for i := 0; i < count; i++ {
		msgs[i] = storage.NewMessage(th.ID, llm.RoleUser, fmt.Sprintf("chunk msg %d", i))
	}

	saved, err := store.SaveMessages(ctx, msgs)
	if err != nil {
		t.Fatalf("SaveMessages 600 failed: %v", err)
	}
	if len(saved) != count {
		t.Fatalf("expected %d saved, got %d", count, len(saved))
	}

	// Test GetMessagesByID chunking
	ids := make([]string, count)
	for i, m := range saved {
		ids[i] = m.ID
	}
	fetched, err := store.GetMessagesByID(ctx, ids)
	if err != nil {
		t.Fatalf("GetMessagesByID 600 failed: %v", err)
	}
	if len(fetched) != count {
		t.Fatalf("expected %d fetched, got %d", count, len(fetched))
	}

	// Test DeleteMessages chunking
	err = store.DeleteMessages(ctx, ids)
	if err != nil {
		t.Fatalf("DeleteMessages 600 failed: %v", err)
	}
	remaining, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages after delete failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining, got %d", len(remaining))
	}
}

func TestStore_MetadataFilteringEdgeCases(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Thread 1: nil metadata
	_, err = store.SaveThread(ctx, &storage.Thread{
		ID:    "th-nil-meta",
		Title: "Nil Meta",
	})
	if err != nil {
		t.Fatalf("SaveThread th-nil-meta failed: %v", err)
	}

	// Thread 2: basic metadata with string, bool, int
	_, err = store.SaveThread(ctx, &storage.Thread{
		ID:    "th-basic-meta",
		Title: "Basic Meta",
		Metadata: map[string]any{
			"env":    "prod",
			"active": true,
			"count":  42,
		},
	})
	if err != nil {
		t.Fatalf("SaveThread th-basic-meta failed: %v", err)
	}

	// Thread 3: complex metadata with slice and nested map
	_, err = store.SaveThread(ctx, &storage.Thread{
		ID:    "th-complex-meta",
		Title: "Complex Meta",
		Metadata: map[string]any{
			"tags": []any{"go", "sqlite"},
			"config": map[string]any{
				"timeout": 30,
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveThread th-complex-meta failed: %v", err)
	}

	// Query 1: Filter by string when a thread with nil metadata exists
	t.Run("string filter with coexisting nil metadata", func(t *testing.T) {
		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"env": "prod"},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		if len(res) != 1 || res[0].ID != "th-basic-meta" {
			t.Fatalf("expected 1 thread th-basic-meta, got %d", len(res))
		}
	})

	// Query 2: Filter by bool
	t.Run("bool filter", func(t *testing.T) {
		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"active": true},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		if len(res) != 1 || res[0].ID != "th-basic-meta" {
			t.Fatalf("expected 1 thread th-basic-meta, got %d", len(res))
		}
	})

	// Query 3: Filter by int
	t.Run("int filter", func(t *testing.T) {
		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"count": 42},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		if len(res) != 1 || res[0].ID != "th-basic-meta" {
			t.Fatalf("expected 1 thread th-basic-meta, got %d", len(res))
		}
	})

	// Query 4: Filter by complex slice
	t.Run("slice filter", func(t *testing.T) {
		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"tags": []any{"go", "sqlite"}},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		if len(res) != 1 || res[0].ID != "th-complex-meta" {
			t.Fatalf("expected 1 thread th-complex-meta, got %d", len(res))
		}
	})

	// Query 5: Filter by nil value (key missing or null on thread with metadata)
	t.Run("nil filter", func(t *testing.T) {
		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"non_existent": nil},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		// th-nil-meta has NO metadata, so it should NOT match. Only threads with metadata match.
		if len(res) != 2 {
			t.Fatalf("expected 2 threads with non_existent=nil, got %d", len(res))
		}
	})

	// Query 6: Filter with special characters in metadata keys (e.g. dots)
	t.Run("special character keys", func(t *testing.T) {
		_, err := store.SaveThread(ctx, &storage.Thread{
			ID:    "th-special-key",
			Title: "Special Key",
			Metadata: map[string]any{
				"app.service.name": "payment-api",
			},
		})
		if err != nil {
			t.Fatalf("SaveThread failed: %v", err)
		}

		res, err := store.ListThreads(ctx, storage.ThreadFilter{
			Metadata: map[string]any{"app.service.name": "payment-api"},
		})
		if err != nil {
			t.Fatalf("ListThreads failed: %v", err)
		}
		if len(res) != 1 || res[0].ID != "th-special-key" {
			t.Fatalf("expected 1 thread th-special-key, got %d", len(res))
		}
	})
}

func TestStore_MultipartMessage_SQLite(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Multimodal Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	imgPart := llm.ImageFromBytes([]byte{10, 20, 30}, "image/png")
	msg := storage.NewMessageWithParts(th.ID, llm.RoleUser, llm.TextPart("Inspect this photo"), imgPart)

	saved, err := store.SaveMessages(ctx, []*storage.Message{msg})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 1 || len(saved[0].Content) != 2 {
		t.Fatalf("unexpected saved message: %+v", saved)
	}

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Content) != 2 {
		t.Fatalf("expected 1 message with 2 parts, got %d", len(msgs))
	}
	if msgs[0].Content[0].Text != "Inspect this photo" || msgs[0].Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected content parts in retrieved message: %+v", msgs[0].Content)
	}
}

func TestStore_MessageThinkingPersistence(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "thinking"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	msg := storage.NewMessage(th.ID, llm.RoleAssistant, "final answer")
	msg.Thinking = "step-by-step reasoning"
	saved, err := store.SaveMessages(ctx, []*storage.Message{msg})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	listed, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(listed) != 1 || listed[0].Thinking != "step-by-step reasoning" {
		t.Fatalf("expected thinking to round-trip, got %+v", listed)
	}

	byID, err := store.GetMessagesByID(ctx, []string{saved[0].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byID) != 1 || byID[0].Thinking != "step-by-step reasoning" {
		t.Errorf("expected thinking from GetMessagesByID, got %+v", byID)
	}
}

func TestStore_MigrateAddsThinkingColumn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Simulate a database created before the thinking column existed.
	if _, err := store.DB().ExecContext(ctx, "ALTER TABLE messages DROP COLUMN thinking"); err != nil {
		t.Fatalf("failed to drop thinking column: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "upgraded"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	msg := storage.NewMessage(th.ID, llm.RoleAssistant, "answer")
	msg.Thinking = "migrated reasoning"
	if _, err := store.SaveMessages(ctx, []*storage.Message{msg}); err != nil {
		t.Fatalf("SaveMessages failed after migration: %v", err)
	}

	listed, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(listed) != 1 || listed[0].Thinking != "migrated reasoning" {
		t.Fatalf("expected thinking after migration, got %+v", listed)
	}
}

func TestStore_PragmasApplyToEveryConnection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pragmas.db")

	store, err := sqlite.New(dbPath,
		sqlite.WithMaxOpenConns(2),
		sqlite.WithBusyTimeout(1234*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Hold both connections at once so the pool must open a second physical
	// connection; the PRAGMAs were previously applied only to the first one.
	conn1, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("failed to acquire first connection: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	conn2, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("failed to acquire second connection: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	for i, conn := range []*sql.Conn{conn1, conn2} {
		var foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d: foreign_keys query failed: %v", i+1, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i+1, foreignKeys)
		}

		var busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d: busy_timeout query failed: %v", i+1, err)
		}
		if busyTimeout != 1234 {
			t.Errorf("connection %d: busy_timeout = %d, want 1234", i+1, busyTimeout)
		}
	}
}
