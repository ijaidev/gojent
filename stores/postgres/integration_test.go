package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/session/sessiontest"
	"github.com/ijaidev/gojent/stores/postgres"
)

// TestIntegration exercises the store against a real PostgreSQL instance.
// Set GOJENT_POSTGRES_DSN to run it; otherwise it is skipped.
func TestIntegration(t *testing.T) {
	dsn := os.Getenv("GOJENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GOJENT_POSTGRES_DSN is not set")
	}

	store, err := postgres.New(dsn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sess := session.Session{
		ID:         fmt.Sprintf("integration-%d", time.Now().UnixNano()),
		ResourceID: "gojent-tests",
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.Delete(cleanup, sess); err != nil {
			t.Errorf("Delete: %v", err)
		}
	})

	messages := []llm.Message{
		llm.UserMessage("hello"),
		canonicalAssistant(t),
		llm.ToolResultsMessage(llm.ToolResult{
			CallID:     "call-1",
			Name:       "lookup",
			Status:     llm.ToolStatusCompleted,
			Content:    []llm.Part{llm.NewTextPart(`{"answer":42}`)},
			Structured: json.RawMessage(`{"answer":42}`),
		}),
	}
	if err := store.Append(ctx, sess, messages...); err != nil {
		t.Fatalf("Append: %v", err)
	}

	loaded, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != len(messages) {
		t.Fatalf("messages = %d, want %d", len(loaded), len(messages))
	}
	var signature map[string]string
	if err := loaded[1].Content[0].Meta().Get("test", &signature); err != nil {
		t.Fatalf("Get provider data: %v", err)
	}
	if signature["sig"] != "x" {
		t.Errorf("provider data did not round-trip: %v", signature)
	}
	calls := loaded[1].ToolCalls()
	if len(calls) != 1 || calls[0].CallID != "call-1" || string(calls[0].Input) != `{"q":"go"}` {
		t.Errorf("tool call did not round-trip: %+v", calls)
	}
	resultPart, ok := loaded[2].Content[0].(*llm.ToolResultPart)
	if !ok {
		t.Fatalf("tool result part = %T", loaded[2].Content[0])
	}
	if resultPart.CallID != "call-1" || resultPart.Text() != `{"answer":42}` ||
		string(resultPart.Structured) != `{"answer":42}` {
		t.Errorf("tool result did not round-trip: %+v", resultPart)
	}

	other := session.Session{ID: sess.ID, ResourceID: "other-tenant"}
	isolated, err := store.Load(ctx, other)
	if err != nil {
		t.Fatalf("Load other: %v", err)
	}
	if len(isolated) != 0 {
		t.Errorf("tenant isolation failed: %d messages", len(isolated))
	}
}

// TestConformance runs the shared store contract against a real PostgreSQL
// database. The table is truncated between subtests, so point
// GOJENT_POSTGRES_DSN at a dedicated test database.
func TestConformance(t *testing.T) {
	dsn := os.Getenv("GOJENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GOJENT_POSTGRES_DSN is not set")
	}

	sessiontest.Run(t, func(t *testing.T) session.Store {
		store, err := postgres.New(dsn)
		if err != nil {
			t.Fatalf("postgres.New: %v", err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
		if _, err := store.DB().ExecContext(context.Background(), "TRUNCATE session_messages RESTART IDENTITY"); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return store
	})
}

// canonicalAssistant builds an assistant turn with per-part replay state,
// generated call IDs, and message-level provider metadata.
func canonicalAssistant(t *testing.T) llm.Message {
	t.Helper()

	reasoning := llm.NewReasoningPart("reasoning")
	if err := reasoning.Meta().Set("test", map[string]string{"sig": "x"}); err != nil {
		t.Fatalf("Set reasoning metadata: %v", err)
	}
	text := llm.NewTextPart("hi")
	if err := text.Meta().Set("test", map[string]string{"encrypted": "secret"}); err != nil {
		t.Fatalf("Set text metadata: %v", err)
	}
	message := llm.AssistantMessageWithParts(
		reasoning,
		text,
		llm.NewToolCallPart(llm.ToolCall{
			ItemID: "item-1",
			CallID: "call-1",
			Name:   "lookup",
			Status: llm.ToolStatusCompleted,
			Input:  json.RawMessage(`{"q":"go"}`),
		}),
	)
	message.Provider = make(llm.ProviderData)
	if err := message.Provider.Set("test", map[string]string{"response_id": "resp-1"}); err != nil {
		t.Fatalf("Set message metadata: %v", err)
	}
	return message
}
