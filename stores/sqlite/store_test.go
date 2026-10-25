package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/stores/sqlite"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.New("")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestStore_AppendAndLoadPreservesOrder(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}

	messages := []llm.Message{
		llm.SystemMessage("be brief"),
		llm.UserMessage("hello"),
		llm.AssistantMessage("hi"),
	}
	for _, message := range messages {
		if err := store.Append(ctx, sess, message); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != len(messages) {
		t.Fatalf("messages = %d, want %d", len(loaded), len(messages))
	}
	for i := range messages {
		if loaded[i].Role != messages[i].Role || loaded[i].Text() != messages[i].Text() {
			t.Errorf("message %d = %+v, want %+v", i, loaded[i], messages[i])
		}
	}
}

func TestStore_IsolatesTenantsAndSessions(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	first := session.Session{ID: "shared", ResourceID: "tenant-a"}
	second := session.Session{ID: "shared", ResourceID: "tenant-b"}
	third := session.Session{ID: "other", ResourceID: "tenant-a"}

	if err := store.Append(ctx, first, llm.UserMessage("a")); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Append(ctx, second, llm.UserMessage("b")); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if err := store.Append(ctx, third, llm.UserMessage("c")); err != nil {
		t.Fatalf("Append third: %v", err)
	}

	for _, tc := range []struct {
		sess session.Session
		want string
	}{
		{first, "a"},
		{second, "b"},
		{third, "c"},
	} {
		messages, err := store.Load(ctx, tc.sess)
		if err != nil {
			t.Fatalf("Load %+v: %v", tc.sess, err)
		}
		if len(messages) != 1 || messages[0].Text() != tc.want {
			t.Errorf("session %+v = %+v, want %q", tc.sess, messages, tc.want)
		}
	}

	empty := session.Session{ID: "missing", ResourceID: "tenant-a"}
	messages, err := store.Load(ctx, empty)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("empty session returned %d messages", len(messages))
	}
}

func TestStore_PersistsReplayState(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "replay"}

	reasoning := llm.NewReasoningPart("reasoning")
	if err := reasoning.Meta().Set("test", map[string]string{"signature": "abc"}); err != nil {
		t.Fatalf("Set reasoning metadata: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "item-1",
		CallID: "call-1",
		Name:   "lookup",
		Status: llm.ToolStatusCompleted,
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := call.Meta().Set("test", map[string]string{"call_signature": "xyz"}); err != nil {
		t.Fatalf("Set call metadata: %v", err)
	}

	assistant := llm.AssistantMessageWithParts(reasoning, llm.NewTextPart("checking"), call)
	assistant.Provider = make(llm.ProviderData)
	if err := assistant.Provider.Set("test", map[string]string{"response_id": "resp-1"}); err != nil {
		t.Fatalf("Set message metadata: %v", err)
	}

	result := llm.ToolResultsMessage(llm.ToolResult{
		CallID:     "call-1",
		Name:       "lookup",
		Status:     llm.ToolStatusCompleted,
		Content:    []llm.Part{llm.NewTextPart(`{"answer":42}`)},
		Structured: json.RawMessage(`{"answer":42}`),
	})

	if err := store.Append(ctx, sess, llm.UserMessage("find"), assistant, result); err != nil {
		t.Fatalf("Append: %v", err)
	}

	loaded, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("messages = %d, want 3", len(loaded))
	}

	assistant = loaded[1]
	if len(assistant.Content) != 3 {
		t.Fatalf("assistant parts = %d, want 3", len(assistant.Content))
	}
	if reasoningPart, ok := assistant.Content[0].(*llm.ReasoningPart); !ok || reasoningPart.Text != "reasoning" {
		t.Errorf("reasoning part = %+v", assistant.Content[0])
	}
	calls := assistant.ToolCalls()
	if len(calls) != 1 || calls[0].ItemID != "item-1" || calls[0].CallID != "call-1" ||
		calls[0].Status != llm.ToolStatusCompleted || string(calls[0].Input) != `{"q":"go"}` {
		t.Errorf("ToolCalls = %+v", calls)
	}
	var signature map[string]string
	if err := assistant.Content[0].Meta().Get("test", &signature); err != nil {
		t.Fatalf("Get reasoning metadata: %v", err)
	}
	if signature["signature"] != "abc" {
		t.Errorf("reasoning metadata = %v", signature)
	}
	var callSignature map[string]string
	if err := call.Meta().Get("test", &callSignature); err != nil {
		t.Fatalf("Get call metadata: %v", err)
	}
	if callSignature["call_signature"] != "xyz" {
		t.Errorf("call metadata = %v", callSignature)
	}
	var responseID map[string]string
	if err := assistant.Provider.Get("test", &responseID); err != nil {
		t.Fatalf("Get message metadata: %v", err)
	}
	if responseID["response_id"] != "resp-1" {
		t.Errorf("message metadata = %v", responseID)
	}

	resultPart, ok := loaded[2].Content[0].(*llm.ToolResultPart)
	if !ok {
		t.Fatalf("tool result part = %T", loaded[2].Content[0])
	}
	if resultPart.CallID != "call-1" || resultPart.Status != llm.ToolStatusCompleted {
		t.Errorf("tool result = %+v", resultPart.ToolResult)
	}
	if string(resultPart.Structured) != `{"answer":42}` || resultPart.Text() != `{"answer":42}` {
		t.Errorf("tool result payload = %+v", resultPart)
	}
}

func TestStore_PersistsFormatVersion(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "version"}

	if err := store.Append(ctx, sess, llm.UserMessage("hello")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var (
		formatVersion int
		encoded       []byte
	)
	if err := store.DB().QueryRowContext(ctx, `
		SELECT format_version, message FROM session_messages
		WHERE resource_id = ? AND session_id = ?`,
		sess.ResourceID, sess.ID).Scan(&formatVersion, &encoded); err != nil {
		t.Fatalf("query persisted row: %v", err)
	}
	if formatVersion != llm.FormatVersion {
		t.Errorf("format_version = %d, want %d", formatVersion, llm.FormatVersion)
	}
	var payload struct {
		FormatVersion int `json:"format_version"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode persisted message: %v", err)
	}
	if payload.FormatVersion != llm.FormatVersion {
		t.Errorf("message format_version = %d, want %d", payload.FormatVersion, llm.FormatVersion)
	}
}

func TestStore_RejectsUnknownFormatVersion(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "old"}

	encoded, err := llm.MarshalMessage(llm.UserMessage("legacy"))
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO session_messages (resource_id, session_id, format_version, message, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		sess.ResourceID, sess.ID, llm.FormatVersion-1, encoded); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if _, err := store.Load(ctx, sess); !errors.Is(err, llm.ErrUnsupportedFormatVersion) {
		t.Fatalf("Load = %v, want ErrUnsupportedFormatVersion", err)
	}
}

func TestStore_AppendIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "atomic"}

	valid := llm.UserMessage("valid")
	// Invalid raw JSON cannot be encoded, so the whole batch must fail.
	invalid := llm.ToolResultsMessage(llm.ToolResult{
		CallID:     "call-1",
		Structured: json.RawMessage(`{`),
	})
	if err := store.Append(ctx, sess, valid, invalid); err == nil {
		t.Fatal("expected an encoding error")
	}

	messages, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("partial append persisted %d messages", len(messages))
	}
}

func TestStore_AppendIsAtomicOnContextCancellation(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "cancel"}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Append(canceled, sess, llm.UserMessage("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append err = %v, want context.Canceled", err)
	}
	if _, err := store.Load(canceled, sess); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load err = %v, want context.Canceled", err)
	}
}

func TestStore_Validation(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	if _, err := store.Load(ctx, session.Session{}); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Load without ID = %v", err)
	}
	if err := store.Append(ctx, session.Session{}, llm.UserMessage("x")); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Append without ID = %v", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrMessagesRequired) {
		t.Errorf("Append without messages = %v", err)
	}
}

func TestStore_Delete(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	sess := session.Session{ID: "delete", ResourceID: "tenant"}
	other := session.Session{ID: "keep", ResourceID: "tenant"}

	if err := store.Append(ctx, sess, llm.UserMessage("gone")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Append(ctx, other, llm.UserMessage("stay")); err != nil {
		t.Fatalf("Append other: %v", err)
	}
	if err := store.Delete(ctx, sess); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	messages, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load deleted: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("deleted session returned %d messages", len(messages))
	}
	messages, err = store.Load(ctx, other)
	if err != nil {
		t.Fatalf("Load other: %v", err)
	}
	if len(messages) != 1 || messages[0].Text() != "stay" {
		t.Errorf("unrelated session changed: %+v", messages)
	}
}

func TestStore_Close(t *testing.T) {
	store := newStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Closing twice is safe.
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	ctx := context.Background()
	if _, err := store.Load(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Load after Close = %v, want ErrClosed", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}, llm.UserMessage("x")); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
	if err := store.Delete(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Delete after Close = %v, want ErrClosed", err)
	}
}

func TestStore_FilePersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	sess := session.Session{ID: "durable", ResourceID: "tenant"}

	first, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := first.Append(ctx, sess, llm.UserMessage("persisted")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := sqlite.New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()
	messages, err := second.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 1 || messages[0].Text() != "persisted" {
		t.Errorf("reopened messages = %+v", messages)
	}
}

func TestNew_EmptyDSNUsesMemory(t *testing.T) {
	store, err := sqlite.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Append(context.Background(), session.Session{ID: "s"}, llm.UserMessage("x")); err != nil {
		t.Fatalf("Append: %v", err)
	}
}
