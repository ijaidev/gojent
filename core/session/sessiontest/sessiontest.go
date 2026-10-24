// Package sessiontest provides a shared conformance suite for session.Store
// implementations.
//
// Backends run it to prove that they honor the two-operation contract: Load
// returns messages in append order, Append is atomic, both operations reject
// invalid sessions, and conversations are isolated by resource scope.
package sessiontest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

// Factory returns a fresh, empty store for one subtest. Implementations should
// register cleanup with t.Cleanup.
type Factory func(t *testing.T) session.Store

// Run executes the conformance suite against the store produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("EmptySession", func(t *testing.T) { testEmptySession(t, newStore(t)) })
	t.Run("AppendAndLoad", func(t *testing.T) { testAppendAndLoad(t, newStore(t)) })
	t.Run("Isolation", func(t *testing.T) { testIsolation(t, newStore(t)) })
	t.Run("AtomicAppend", func(t *testing.T) { testAtomicAppend(t, newStore(t)) })
	t.Run("ReplayState", func(t *testing.T) { testReplayState(t, newStore(t)) })
	t.Run("Validation", func(t *testing.T) { testValidation(t, newStore(t)) })
	t.Run("ContextCancellation", func(t *testing.T) { testContextCancellation(t, newStore(t)) })
}

func testEmptySession(t *testing.T, store session.Store) {
	ctx := context.Background()
	messages, err := store.Load(ctx, session.Session{ID: "empty", ResourceID: "tenant"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("empty session returned %d messages", len(messages))
	}
}

func testAppendAndLoad(t *testing.T, store session.Store) {
	ctx := context.Background()
	sess := session.Session{ID: "conversation", ResourceID: "tenant"}

	first := []llm.Message{llm.SystemMessage("be brief"), llm.UserMessage("hello")}
	if err := store.Append(ctx, sess, first...); err != nil {
		t.Fatalf("Append first batch: %v", err)
	}
	second := []llm.Message{llm.AssistantMessage("hi"), llm.UserMessage("again")}
	if err := store.Append(ctx, sess, second...); err != nil {
		t.Fatalf("Append second batch: %v", err)
	}

	messages, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := append(append([]llm.Message(nil), first...), second...)
	if len(messages) != len(want) {
		t.Fatalf("messages = %d, want %d", len(messages), len(want))
	}
	for i := range want {
		if messages[i].Role != want[i].Role || messages[i].Text() != want[i].Text() {
			t.Errorf("message %d = %+v, want %+v", i, messages[i], want[i])
		}
	}
}

func testIsolation(t *testing.T, store session.Store) {
	ctx := context.Background()
	first := session.Session{ID: "shared", ResourceID: "tenant-a"}
	second := session.Session{ID: "shared", ResourceID: "tenant-b"}

	if err := store.Append(ctx, first, llm.UserMessage("a")); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Append(ctx, second, llm.UserMessage("b")); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	assertSingleMessage(t, store, first, "a")
	assertSingleMessage(t, store, second, "b")
}

func assertSingleMessage(t *testing.T, store session.Store, sess session.Session, want string) {
	t.Helper()
	messages, err := store.Load(context.Background(), sess)
	if err != nil {
		t.Fatalf("Load %+v: %v", sess, err)
	}
	if len(messages) != 1 || messages[0].Text() != want {
		t.Fatalf("session %+v = %+v, want one message %q", sess, messages, want)
	}
}

func testAtomicAppend(t *testing.T, store session.Store) {
	ctx := context.Background()
	sess := session.Session{ID: "atomic", ResourceID: "tenant"}

	valid := llm.UserMessage("valid")
	// Invalid raw JSON cannot be encoded by a JSON-backed store. If a backend
	// accepts it, the suite cannot exercise a mid-append failure.
	invalid := llm.Message{Role: llm.RoleTool, Data: json.RawMessage(`{`)}
	if err := store.Append(ctx, sess, valid, invalid); err == nil {
		t.Skip("store accepted unencodable message; no failure injection available")
	}

	messages, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("failed append persisted %d messages", len(messages))
	}
}

func testReplayState(t *testing.T, store session.Store) {
	ctx := context.Background()
	sess := session.Session{ID: "replay", ResourceID: "tenant"}
	original := llm.Message{
		Role:      llm.RoleAssistant,
		Thinking:  "reasoning",
		ToolCalls: []llm.ToolCall{{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`}},
		ProviderData: &llm.ProviderData{
			Provider: "test",
			Version:  2,
			Data:     []byte(`{"signature":"abc"}`),
		},
	}
	if err := store.Append(ctx, sess, llm.UserMessage("find"), original); err != nil {
		t.Fatalf("Append: %v", err)
	}

	messages, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	assistant := messages[1]
	if assistant.Thinking != "reasoning" {
		t.Errorf("Thinking = %q", assistant.Thinking)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Errorf("ToolCalls = %+v", assistant.ToolCalls)
	}
	if assistant.ProviderData == nil || assistant.ProviderData.Provider != "test" || assistant.ProviderData.Version != 2 {
		t.Fatalf("ProviderData = %+v", assistant.ProviderData)
	}
	if string(assistant.ProviderData.Data) != `{"signature":"abc"}` {
		t.Errorf("ProviderData.Data = %s", assistant.ProviderData.Data)
	}

	// Loaded messages must not alias store state.
	if len(assistant.ToolCalls) > 0 {
		assistant.ToolCalls[0].Arguments = `{"q":"mutated"}`
	}
	if assistant.ProviderData != nil && len(assistant.ProviderData.Data) > 0 {
		assistant.ProviderData.Data[0] = '['
	}
	reloaded, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if reloaded[1].ToolCalls[0].Arguments != `{"q":"go"}` {
		t.Error("mutating loaded tool calls changed store state")
	}
	if string(reloaded[1].ProviderData.Data) != `{"signature":"abc"}` {
		t.Error("mutating loaded provider data changed store state")
	}
}

func testValidation(t *testing.T, store session.Store) {
	ctx := context.Background()

	if _, err := store.Load(ctx, session.Session{}); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Load without ID = %v, want ErrIDRequired", err)
	}
	if err := store.Append(ctx, session.Session{}, llm.UserMessage("x")); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Append without ID = %v, want ErrIDRequired", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrNilStoreMessage) {
		t.Errorf("Append without messages = %v, want ErrNilStoreMessage", err)
	}
}

func testContextCancellation(t *testing.T, store session.Store) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Load(ctx, session.Session{ID: "s"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Load with canceled context = %v, want context.Canceled", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}, llm.UserMessage("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("Append with canceled context = %v, want context.Canceled", err)
	}
}
