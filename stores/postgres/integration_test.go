package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
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
		{
			Role:         llm.RoleAssistant,
			Content:      []llm.Part{llm.TextPart("hi")},
			ToolCalls:    []llm.ToolCall{{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`}},
			ProviderData: &llm.ProviderData{Provider: "test", Version: 1, Data: []byte(`{"sig":"x"}`)},
		},
		{Role: llm.RoleTool, ToolCallID: "call-1", Name: "lookup", Content: []llm.Part{llm.TextPart(`{"answer":42}`)}},
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
	if loaded[1].ProviderData == nil || string(loaded[1].ProviderData.Data) != `{"sig":"x"}` {
		t.Errorf("provider data did not round-trip: %+v", loaded[1].ProviderData)
	}
	if loaded[2].ToolCallID != "call-1" || loaded[2].Text() != `{"answer":42}` {
		t.Errorf("tool result did not round-trip: %+v", loaded[2])
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
