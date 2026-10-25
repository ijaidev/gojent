package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
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
		Payload:   llm.UserMessage("run the tests"),
		CreatedAt: now,
	}
	assistant := &app.Message{
		ID:       "msg_assistant",
		ThreadID: thread.ID,
		Payload: llm.AssistantMessageWithParts(
			llm.NewTextPart("I will run them."),
			llm.NewToolCallPart(llm.ToolCall{
				CallID: "call_1",
				Name:   "execute_command",
				Input:  json.RawMessage(`{"command":"go test ./..."}`),
			}),
		),
		CreatedAt: now.Add(time.Millisecond),
	}
	tool := &app.Message{
		ID:       "msg_tool",
		ThreadID: thread.ID,
		Payload: llm.ToolResultsMessage(llm.ToolResult{
			CallID:  "call_1",
			Name:    "execute_command",
			Status:  llm.ToolStatusCompleted,
			Content: []llm.Part{llm.NewTextPart("ok")},
		}),
		CreatedAt: now.Add(2 * time.Millisecond),
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
	calls := messages[1].ToolCalls()
	if len(calls) != 1 || calls[0].Name != "execute_command" || calls[0].CallID != "call_1" {
		t.Fatalf("tool calls = %+v", calls)
	}
	results := messages[2].ToolResults()
	if len(results) != 1 || results[0].CallID != "call_1" || results[0].Name != "execute_command" {
		t.Fatalf("tool results = %+v", results)
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

func TestMessageReplayStateRoundTrip(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	thread := testThread()
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	reasoning := llm.NewReasoningPart("considering")
	reasoning.Summary = []string{"first", "second"}
	if err := reasoning.Set("google", map[string]string{"thought_signature": "c2lnbmF0dXJl"}); err != nil {
		t.Fatalf("reasoning.Set: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "item_1",
		CallID: "call_1",
		Name:   "lookup",
		Kind:   llm.ToolKindFunction,
		Input:  json.RawMessage(`{"query":"go"}`),
		Status: llm.ToolStatusCompleted,
	})
	if err := call.Set("openai", map[string]string{"item_id": "item_1"}); err != nil {
		t.Fatalf("call.Set: %v", err)
	}
	file := llm.FileFromBytes([]byte("image-bytes"), "image/png")
	file.Filename = "diagram.png"

	assistant := &app.Message{
		ID:       "msg_assistant",
		ThreadID: thread.ID,
		Payload: llm.Message{
			Role:    llm.RoleAssistant,
			Content: []llm.Part{reasoning, llm.NewTextPart("calling the tool"), call, file},
			Status:  llm.MessageStatusCompleted,
			Phase:   "final_answer",
		},
		CreatedAt: time.Now().UTC(),
	}

	success := llm.NewToolResultPart(llm.ToolResult{
		CallID:     "call_1",
		Name:       "lookup",
		Kind:       llm.ToolKindFunction,
		Status:     llm.ToolStatusCompleted,
		Content:    []llm.Part{llm.NewTextPart("found"), llm.FileFromURL("https://example.test/out.png", "image/png")},
		Structured: json.RawMessage(`{"count":1}`),
	})
	if err := success.Set("google", map[string]string{"function_response_id": "resp_1"}); err != nil {
		t.Fatalf("success.Set: %v", err)
	}
	missing := llm.NewToolResultPart(llm.ToolResult{
		CallID:        "call_2",
		Name:          "ask",
		Status:        llm.ToolStatusInputRequired,
		InputRequests: json.RawMessage(`{"prompt":"name"}`),
	})
	results := &app.Message{
		ID:        "msg_results",
		ThreadID:  thread.ID,
		Payload:   llm.Message{Role: llm.RoleUser, Content: []llm.Part{success, missing}},
		CreatedAt: time.Now().UTC(),
	}

	if err := store.AppendMessages(ctx, assistant, results); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}
	messages, err := store.ListMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	assertSamePayload(t, assistant, messages[0])
	assertSamePayload(t, results, messages[1])

	gotResults := messages[1].ToolResults()
	if len(gotResults) != 2 {
		t.Fatalf("tool results = %+v", gotResults)
	}
	if gotResults[0].Status != llm.ToolStatusCompleted ||
		string(gotResults[0].Structured) != `{"count":1}` {
		t.Fatalf("structured result = %+v", gotResults[0])
	}
	if gotResults[1].Status != llm.ToolStatusInputRequired ||
		string(gotResults[1].InputRequests) != `{"prompt":"name"}` {
		t.Fatalf("input-required result = %+v", gotResults[1])
	}
	var signature map[string]string
	if err := messages[0].Payload.Content[0].Meta().Get("google", &signature); err != nil {
		t.Fatalf("read reasoning metadata: %v", err)
	}
	if signature["thought_signature"] != "c2lnbmF0dXJl" {
		t.Fatalf("reasoning metadata = %+v", signature)
	}
}

func TestListMessagesRejectsUnknownFormatVersion(t *testing.T) {
	current, err := llm.MarshalMessage(llm.UserMessage("current"))
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	legacy, err := json.Marshal(llm.UserMessage("legacy"))
	if err != nil {
		t.Fatalf("marshal legacy message: %v", err)
	}

	cases := []struct {
		name          string
		columnVersion int
		payload       string
	}{
		{name: "column", columnVersion: llm.FormatVersion + 1, payload: string(current)},
		{name: "payload", columnVersion: llm.FormatVersion, payload: string(legacy)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			ctx := context.Background()
			thread := testThread()
			if err := store.CreateThread(ctx, thread); err != nil {
				t.Fatalf("CreateThread: %v", err)
			}
			if _, err := store.db.ExecContext(ctx, `
				INSERT INTO messages (id, thread_id, format_version, message, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				"msg_old", thread.ID, tc.columnVersion, tc.payload,
				formatTime(time.Now().UTC()),
			); err != nil {
				t.Fatalf("insert legacy message: %v", err)
			}

			_, err := store.ListMessages(ctx, thread.ID)
			if !errors.Is(err, llm.ErrUnsupportedFormatVersion) {
				t.Fatalf("ListMessages error = %v, want %v", err, llm.ErrUnsupportedFormatVersion)
			}
		})
	}
}

func assertSamePayload(t *testing.T, want, got *app.Message) {
	t.Helper()
	wantJSON, err := llm.MarshalMessage(want.Payload)
	if err != nil {
		t.Fatalf("marshal want payload: %v", err)
	}
	gotJSON, err := llm.MarshalMessage(got.Payload)
	if err != nil {
		t.Fatalf("marshal got payload: %v", err)
	}
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("payload mismatch:\nwant %s\n got %s", wantJSON, gotJSON)
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
		Payload:  llm.UserMessage("hello"),
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
		Payload:   llm.UserMessage("persist me"),
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
