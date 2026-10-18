package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func TestMockStorage_ThreadLifecycle(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
	defer func() { _ = store.Close() }()

	// 1. Create a thread without ID (auto-generates)
	th1, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "First Conversation",
		ResourceID: "user-123",
		Metadata: map[string]any{
			"env": "test",
		},
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if th1.ID == "" {
		t.Fatalf("expected auto-generated thread ID, got empty")
	}
	if th1.Title != "First Conversation" {
		t.Errorf("expected Title 'First Conversation', got %q", th1.Title)
	}
	if th1.CreatedAt.IsZero() || th1.UpdatedAt.IsZero() {
		t.Errorf("expected non-zero timestamps")
	}

	// 2. Retrieve thread by ID
	retrieved, err := store.GetThreadByID(ctx, th1.ID)
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if retrieved.ID != th1.ID || retrieved.Title != th1.Title {
		t.Errorf("retrieved thread mismatch: %+v vs %+v", retrieved, th1)
	}

	// 3. Update thread title and metadata
	newTitle := "Updated Conversation"
	updated, err := store.UpdateThread(ctx, th1.ID, storage.UpdateThreadInput{
		Title: &newTitle,
		Metadata: map[string]any{
			"status": "active",
		},
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected updated title %q, got %q", newTitle, updated.Title)
	}
	if updated.Metadata["env"] != "test" || updated.Metadata["status"] != "active" {
		t.Errorf("expected merged metadata, got %+v", updated.Metadata)
	}

	// 4. Create second thread for listing test
	th2, err := store.SaveThread(ctx, &storage.Thread{
		ID:         "custom-th-2",
		Title:      "Second Thread",
		ResourceID: "user-456",
		Metadata: map[string]any{
			"env": "prod",
		},
	})
	if err != nil {
		t.Fatalf("SaveThread th2 failed: %v", err)
	}
	if th2.ID != "custom-th-2" {
		t.Errorf("expected custom ID 'custom-th-2', got %q", th2.ID)
	}

	// 5. List threads with filter
	list1, err := store.ListThreads(ctx, storage.ThreadFilter{
		ResourceID: "user-123",
	})
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	if len(list1) != 1 || list1[0].ID != th1.ID {
		t.Errorf("expected 1 thread for user-123, got %d", len(list1))
	}

	// Filter by metadata
	listMeta, err := store.ListThreads(ctx, storage.ThreadFilter{
		Metadata: map[string]any{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("ListThreads by metadata failed: %v", err)
	}
	if len(listMeta) != 1 || listMeta[0].ID != "custom-th-2" {
		t.Errorf("expected 1 thread with env=prod, got %d", len(listMeta))
	}

	// 6. Delete thread
	err = store.DeleteThread(ctx, th1.ID)
	if err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	// Confirm thread is gone
	_, err = store.GetThreadByID(ctx, th1.ID)
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestMockStorage_MessageLifecycle(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
	defer func() { _ = store.Close() }()

	// Create parent thread
	th, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "Chat Thread",
		ResourceID: "user-1",
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Save messages
	msg1 := storage.NewMessage(th.ID, llm.RoleUser, "Hello agent")
	msg2 := storage.NewMessage(th.ID, llm.RoleAssistant, "Hello human!")
	msg2.ToolCalls = []llm.ToolCall{
		{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`},
	}

	saved, err := store.SaveMessages(ctx, []*storage.Message{msg1, msg2})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved messages, got %d", len(saved))
	}
	if saved[0].ID == "" || saved[1].ID == "" {
		t.Fatalf("expected generated IDs for saved messages")
	}
	if saved[0].ResourceID != "user-1" {
		t.Errorf("expected inherited resourceID 'user-1', got %q", saved[0].ResourceID)
	}

	// List messages for thread
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID:  th.ID,
		SortOrder: storage.SortAsc,
	})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text() != "Hello agent" || msgs[1].Text() != "Hello human!" {
		t.Errorf("unexpected message order or content")
	}

	// Test converter to LLM messages
	llmMsgs := storage.ToLLMMessages(msgs)
	if len(llmMsgs) != 2 {
		t.Fatalf("expected 2 LLM messages, got %d", len(llmMsgs))
	}
	if llmMsgs[0].Role != llm.RoleUser || llmMsgs[1].Role != llm.RoleAssistant {
		t.Errorf("unexpected LLM roles: %v, %v", llmMsgs[0].Role, llmMsgs[1].Role)
	}
	if len(llmMsgs[1].ToolCalls) != 1 || llmMsgs[1].ToolCalls[0].ID != "tc-1" {
		t.Errorf("tool calls not preserved in ToLLMMessage")
	}

	// Test retrieval by IDs
	byIDs, err := store.GetMessagesByID(ctx, []string{saved[0].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byIDs) != 1 || byIDs[0].ID != saved[0].ID {
		t.Errorf("expected 1 message matching ID, got %+v", byIDs)
	}

	// Delete message
	err = store.DeleteMessages(ctx, []string{saved[0].ID})
	if err != nil {
		t.Fatalf("DeleteMessages failed: %v", err)
	}
	remaining, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages after delete failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != saved[1].ID {
		t.Errorf("expected remaining message msg2, got %+v", remaining)
	}
}

func TestMockStorage_ThreadStateLifecycle(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
	defer func() { _ = store.Close() }()

	threadID := "thread-state-test"

	// 1. Get non-existent state
	_, err := store.GetThreadState(ctx, threadID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound, got %v", err)
	}

	// 2. Set state
	tasks := []map[string]any{
		{"id": "t1", "content": "Write docs", "status": "pending"},
	}
	err = store.SetThreadState(ctx, threadID, "task", tasks)
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// Set another state type
	err = store.SetThreadState(ctx, threadID, "goal", map[string]any{
		"objective": "Build memory system",
		"status":    "active",
	})
	if err != nil {
		t.Fatalf("SetThreadState goal failed: %v", err)
	}

	// 3. Get state
	val, err := store.GetThreadState(ctx, threadID, "task")
	if err != nil {
		t.Fatalf("GetThreadState failed: %v", err)
	}
	gotTasks, ok := val.([]map[string]any)
	if !ok || len(gotTasks) != 1 || gotTasks[0]["id"] != "t1" {
		t.Errorf("unexpected state value retrieved: %+v", val)
	}

	// 4. List all thread states
	states, err := store.ListThreadStates(ctx, threadID)
	if err != nil {
		t.Fatalf("ListThreadStates failed: %v", err)
	}
	if len(states) != 2 {
		t.Errorf("expected 2 states, got %d", len(states))
	}
	if states["task"] == nil || states["goal"] == nil {
		t.Errorf("expected task and goal in states list")
	}

	// 5. Delete state
	err = store.DeleteThreadState(ctx, threadID, "task")
	if err != nil {
		t.Fatalf("DeleteThreadState failed: %v", err)
	}
	_, err = store.GetThreadState(ctx, threadID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after delete, got %v", err)
	}
}

func TestMockStorage_CascadeDelete(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "To be deleted"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	// Add messages to thread
	_, err = store.SaveMessages(ctx, []*storage.Message{
		storage.NewMessage(th.ID, llm.RoleUser, "msg1"),
		storage.NewMessage(th.ID, llm.RoleAssistant, "msg2"),
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	// Add state to thread
	err = store.SetThreadState(ctx, th.ID, "task", "some-task")
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// Delete thread
	err = store.DeleteThread(ctx, th.ID)
	if err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	// Verify messages are cascaded
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after thread delete, got %d", len(msgs))
	}

	// Verify state is cascaded
	_, err = store.GetThreadState(ctx, th.ID, "task")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound after thread delete, got %v", err)
	}
}

func TestMockStorage_ContextCancellationAndClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	store := storage.NewMockStorage()
	_, err := store.SaveThread(ctx, &storage.Thread{Title: "Cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	activeCtx := context.Background()
	_ = store.Close()

	_, err = store.SaveThread(activeCtx, &storage.Thread{Title: "Closed"})
	if !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	_, err = store.GetThreadByID(activeCtx, "id")
	if !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	err = store.DeleteThread(activeCtx, "id")
	if !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

func TestMockStorage_MessageUpdateNoDuplicatesAndTimestampPreservation(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
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

func TestMockStorage_UpdateThread_ResourceID(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
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

func TestStorage_MultipartMessage(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStorage()
	defer func() { _ = store.Close() }()

	th, err := store.SaveThread(ctx, &storage.Thread{Title: "Multimodal Thread"})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}

	imgPart := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	msg := storage.NewMessageWithParts(th.ID, llm.RoleUser, llm.TextPart("Look at this"), imgPart)

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
	if msgs[0].Content[0].Text != "Look at this" || msgs[0].Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected content in loaded message: %+v", msgs[0].Content)
	}
}
