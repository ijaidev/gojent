package storage_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func TestMessageFromLLM_RoundTrip(t *testing.T) {
	original := llm.Message{
		Role:       llm.RoleAssistant,
		Content:    []llm.Part{llm.TextPart("answer")},
		Thinking:   "reasoning",
		Name:       "assistant",
		ToolCalls:  []llm.ToolCall{{ID: "tc-1", Name: "search", Arguments: `{"q":"x"}`}},
		ToolCallID: "call-1",
		Raw:        map[string]any{"signature": "abc"},
	}

	msg := storage.NewMessageFromLLM("thread-1", original)
	if msg.ThreadID != "thread-1" {
		t.Errorf("expected thread ID thread-1, got %q", msg.ThreadID)
	}
	if msg.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	roundTripped := storage.ToLLMMessages([]*storage.Message{msg})
	if len(roundTripped) != 1 {
		t.Fatalf("expected 1 message, got %d", len(roundTripped))
	}

	got := roundTripped[0]
	if got.Role != original.Role || got.Thinking != original.Thinking || got.Name != original.Name {
		t.Errorf("message fields not preserved: %+v", got)
	}
	if got.ToolCallID != original.ToolCallID {
		t.Errorf("tool call ID not preserved: %q", got.ToolCallID)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "answer" {
		t.Errorf("content not preserved: %+v", got.Content)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "tc-1" {
		t.Errorf("tool calls not preserved: %+v", got.ToolCalls)
	}
	if got.Raw == nil {
		t.Error("raw payload not preserved")
	}
}

func TestToLLMMessages_HandlesNils(t *testing.T) {
	if got := storage.ToLLMMessages(nil); got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}

	got := storage.ToLLMMessages([]*storage.Message{
		nil,
		storage.NewMessage("thread-1", llm.RoleUser, "hi"),
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != "" || len(got[0].Content) != 0 {
		t.Errorf("expected zero message for nil entry, got %+v", got[0])
	}
	if got[1].Role != llm.RoleUser || len(got[1].Content) != 1 {
		t.Errorf("unexpected message for valid entry: %+v", got[1])
	}
}

func TestMessageClone_CopiesContentAndTopLevelMetadata(t *testing.T) {
	original := storage.NewMessageWithParts("thread-1", llm.RoleAssistant,
		llm.TextPart("hello"),
		llm.ImageFromBytes([]byte{1, 2, 3}, "image/png"),
	)
	original.Thinking = "thought"
	original.ToolCalls = []llm.ToolCall{{ID: "tc-1", Name: "search"}}
	original.Metadata = map[string]any{"key": "value"}

	clone := original.Clone()
	clone.Content[0].Text = "changed"
	clone.Content[1].Data[0] = 9
	clone.ToolCalls[0].Name = "changed"
	clone.Metadata["key"] = "changed"

	if original.Content[0].Text != "hello" {
		t.Error("content mutation leaked into the original")
	}
	if original.Content[1].Data[0] != 1 {
		t.Error("content data mutation leaked into the original")
	}
	if original.ToolCalls[0].Name != "search" {
		t.Error("tool call mutation leaked into the original")
	}
	if original.Metadata["key"] != "value" {
		t.Error("top-level metadata mutation leaked into the original")
	}
}

func TestMessageText_JoinsTextParts(t *testing.T) {
	msg := storage.NewMessageWithParts("thread-1", llm.RoleUser,
		llm.TextPart("first"), llm.TextPart("second"))
	if got := msg.Text(); got != "first\nsecond" {
		t.Errorf("expected %q, got %q", "first\nsecond", got)
	}

	var nilMsg *storage.Message
	if got := nilMsg.Text(); got != "" {
		t.Errorf("expected empty text for nil message, got %q", got)
	}
}
