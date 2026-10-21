package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/core/llm"
)

func newTestStore(t *testing.T) (store *Store, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "codjent.db")
	var err error
	store, err = New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store, path
}

func testThread() *app.Thread {
	now := time.Date(2026, 10, 21, 12, 0, 0, 123, time.UTC)
	return &app.Thread{
		ID:         "th_test",
		Title:      "Test thread",
		Provider:   "google",
		Model:      "gemini-3.8-flash",
		AccessMode: app.AccessAsk,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestThreadLifecycle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	thread := testThread()

	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	got, err := store.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got.ID != thread.ID || got.Title != thread.Title || got.Provider != thread.Provider || got.Model != thread.Model {
		t.Fatalf("GetThread = %+v, want %+v", got, thread)
	}
	if got.AccessMode != app.AccessAsk {
		t.Fatalf("AccessMode = %q, want %q", got.AccessMode, app.AccessAsk)
	}
	if !got.CreatedAt.Equal(thread.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, thread.CreatedAt)
	}

	threads, err := store.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != thread.ID {
		t.Fatalf("ListThreads = %+v", threads)
	}

	updated := time.Now().UTC()
	thread.Title = "Renamed"
	thread.AccessMode = app.AccessFull
	thread.UpdatedAt = updated
	if err := store.UpdateThread(ctx, thread); err != nil {
		t.Fatalf("UpdateThread: %v", err)
	}
	got, err = store.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread after update: %v", err)
	}
	if got.Title != "Renamed" || got.AccessMode != app.AccessFull {
		t.Fatalf("updated thread = %+v", got)
	}

	if err := store.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if _, err := store.GetThread(ctx, thread.ID); !errors.Is(err, app.ErrThreadNotFound) {
		t.Fatalf("GetThread after delete error = %v, want ErrThreadNotFound", err)
	}
	threads, err = store.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads after delete: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("ListThreads after delete = %+v, want empty", threads)
	}
}

func TestMessageLifecycle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	thread := testThread()
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	now := time.Now().UTC()
	user := &app.Message{
		ID:        "msg_user",
		ThreadID:  thread.ID,
		Role:      llm.RoleUser,
		Content:   []llm.Part{llm.TextPart("run the tests")},
		CreatedAt: now,
	}
	assistant := &app.Message{
		ID:       "msg_assistant",
		ThreadID: thread.ID,
		Role:     llm.RoleAssistant,
		Content:  []llm.Part{llm.TextPart("I will run them.")},
		ToolCalls: []llm.ToolCall{{
			ID:        "call_1",
			Name:      "execute_command",
			Arguments: `{"command":"go test ./..."}`,
		}},
		CreatedAt: now.Add(time.Millisecond),
	}
	tool := &app.Message{
		ID:         "msg_tool",
		ThreadID:   thread.ID,
		Role:       llm.RoleTool,
		Name:       "execute_command",
		ToolCallID: "call_1",
		Content:    []llm.Part{llm.TextPart("ok")},
		CreatedAt:  now.Add(2 * time.Millisecond),
	}
	if err := store.AppendMessages(ctx, user, assistant, tool); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	messages, err := store.ListMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("ListMessages len = %d, want 3", len(messages))
	}
	if messages[0].ID != user.ID || messages[1].ID != assistant.ID || messages[2].ID != tool.ID {
		t.Fatalf("message order = %q, %q, %q", messages[0].ID, messages[1].ID, messages[2].ID)
	}
	if got := messages[1].Text(); got != "I will run them." {
		t.Fatalf("assistant text = %q", got)
	}
	if len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].Name != "execute_command" {
		t.Fatalf("tool calls = %+v", messages[1].ToolCalls)
	}
	if messages[2].ToolCallID != "call_1" || messages[2].Name != "execute_command" {
		t.Fatalf("tool message = %+v", messages[2])
	}

	if err := store.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	messages, err = store.ListMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListMessages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after cascade delete = %+v", messages)
	}
}

func TestNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.GetThread(ctx, "missing"); !errors.Is(err, app.ErrThreadNotFound) {
		t.Fatalf("GetThread error = %v", err)
	}
	thread := testThread()
	if err := store.UpdateThread(ctx, thread); !errors.Is(err, app.ErrThreadNotFound) {
		t.Fatalf("UpdateThread error = %v", err)
	}
	if err := store.DeleteThread(ctx, "missing"); !errors.Is(err, app.ErrThreadNotFound) {
		t.Fatalf("DeleteThread error = %v", err)
	}
	err := store.AppendMessages(ctx, &app.Message{
		ID:       "msg_missing",
		ThreadID: "missing",
		Role:     llm.RoleUser,
		Content:  []llm.Part{llm.TextPart("hello")},
	})
	if !errors.Is(err, app.ErrThreadNotFound) {
		t.Fatalf("AppendMessages error = %v", err)
	}
	if err := store.AppendMessages(ctx, nil); !errors.Is(err, app.ErrInvalidInput) {
		t.Fatalf("AppendMessages nil error = %v", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codjent.db")
	ctx := context.Background()

	store, err := New(ctx, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	thread := testThread()
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if err := store.AppendMessages(ctx, &app.Message{
		ID:        "msg_1",
		ThreadID:  thread.ID,
		Role:      llm.RoleUser,
		Content:   []llm.Part{llm.TextPart("persist me")},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := New(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, err := reopened.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread after reopen: %v", err)
	}
	if got.Title != thread.Title {
		t.Fatalf("thread title = %q", got.Title)
	}
	messages, err := reopened.ListMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListMessages after reopen: %v", err)
	}
	if len(messages) != 1 || messages[0].Text() != "persist me" {
		t.Fatalf("messages after reopen = %+v", messages)
	}
}

func TestDatabaseFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	store, path := newTestStore(t)
	_ = store
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("database mode = %o, want 600", perm)
	}
}
