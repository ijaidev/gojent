package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMessageViewsProjectAssistantParts(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	reasoning := llm.NewReasoningPart("plan")
	reasoning.Summary = []string{"summary"}
	if err := reasoning.Set("google", map[string]string{"thought_signature": "c2ln"}); err != nil {
		t.Fatalf("reasoning.Set: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "item_1",
		CallID: "call_1",
		Name:   "lookup",
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	server := llm.NewToolCallPart(llm.ToolCall{
		CallID: "call_server",
		Name:   "web_search",
		Kind:   llm.ToolKindServer,
	})
	file := llm.FileFromBytes([]byte("image-bytes"), "image/png")
	file.Filename = "diagram.png"

	message := &Message{
		ID:        "msg_assistant",
		ThreadID:  "th_1",
		CreatedAt: now,
		Payload: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.Part{
				reasoning,
				llm.NewTextPart("answer"),
				call,
				server,
				file,
				llm.NewRefusalPart("declined"),
			},
		},
	}

	views := message.Views()
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	view := views[0]
	if view.ID != message.ID || view.ThreadID != message.ThreadID ||
		view.Role != string(llm.RoleAssistant) || !view.CreatedAt.Equal(now) {
		t.Fatalf("view envelope = %+v", view)
	}
	if view.Thinking != "plan\nsummary" {
		t.Fatalf("thinking = %q", view.Thinking)
	}
	if len(view.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", view.ToolCalls)
	}
	if callView := view.ToolCalls[0]; callView.ID != "call_1" ||
		callView.Name != "lookup" || callView.Arguments != `{"q":"go"}` {
		t.Fatalf("tool call view = %+v", callView)
	}
	if len(view.Content) != 3 {
		t.Fatalf("content = %+v", view.Content)
	}
	if view.Content[0].Type != llm.PartTypeText || view.Content[0].Text != "answer" {
		t.Fatalf("text view = %+v", view.Content[0])
	}
	if view.Content[1].Type != llm.PartTypeFile ||
		view.Content[1].MIMEType != "image/png" ||
		string(view.Content[1].Data) != "image-bytes" {
		t.Fatalf("file view = %+v", view.Content[1])
	}
	if view.Content[2].Type != llm.PartTypeRefusal || view.Content[2].Text != "declined" {
		t.Fatalf("refusal view = %+v", view.Content[2])
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"provider", "thought_signature", "web_search", "call_server", "filename", "item_1"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("view JSON exposes %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{`"thinking"`, `"tool_calls"`, `"arguments"`, `"mime_type"`, `"data"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("view JSON missing %s: %s", required, text)
		}
	}
}

func TestMessageViewsExpandToolResults(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	resultOne := llm.NewToolResultPart(llm.ToolResult{
		CallID:  "call_1",
		Name:    "lookup",
		Status:  llm.ToolStatusCompleted,
		Content: []llm.Part{llm.NewTextPart("found")},
	})
	resultTwo := llm.NewToolResultPart(llm.ToolResult{
		CallID: "call_2",
		Name:   "ask",
		Status: llm.ToolStatusError,
		Content: []llm.Part{
			llm.NewTextPart("failed"),
			llm.FileFromURL("https://example.test/out.png", "image/png"),
		},
	})
	message := &Message{
		ID:        "msg_user",
		ThreadID:  "th_1",
		CreatedAt: now,
		Payload: llm.Message{
			Role:    llm.RoleUser,
			Content: []llm.Part{llm.NewTextPart("context"), resultOne, resultTwo, llm.NewTextPart("trailing")},
		},
	}

	views := message.Views()
	if len(views) != 4 {
		t.Fatalf("views = %+v", views)
	}
	if views[0].Role != string(llm.RoleUser) || views[0].ID != message.ID ||
		views[0].Content[0].Text != "context" {
		t.Fatalf("text view = %+v", views[0])
	}
	if views[1].Role != "tool" || views[1].ID != "msg_user:result:1" ||
		views[1].ToolCallID != "call_1" || views[1].Name != "lookup" ||
		views[1].Content[0].Text != "found" {
		t.Fatalf("first tool view = %+v", views[1])
	}
	if views[2].Role != "tool" || views[2].ID != "msg_user:result:2" ||
		views[2].ToolCallID != "call_2" ||
		views[2].Content[1].URL != "https://example.test/out.png" {
		t.Fatalf("second tool view = %+v", views[2])
	}
	if views[3].Role != string(llm.RoleUser) || views[3].ID != "msg_user:text:1" ||
		views[3].Content[0].Text != "trailing" {
		t.Fatalf("trailing text view = %+v", views[3])
	}
	seen := make(map[string]struct{}, len(views))
	for _, view := range views {
		if _, ok := seen[view.ID]; ok {
			t.Fatalf("duplicate view ID %q", view.ID)
		}
		seen[view.ID] = struct{}{}
	}

	if _, err := json.Marshal(message); err == nil {
		t.Fatal("marshaling a multi-view message should fail")
	}

	detail := ThreadDetail{
		Thread:   &Thread{ID: "th_1"},
		Messages: []*Message{message},
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal thread detail: %v", err)
	}
	var decoded struct {
		Messages []MessageView `json:"messages"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode thread detail: %v", err)
	}
	if len(decoded.Messages) != 4 || decoded.Messages[1].ToolCallID != "call_1" {
		t.Fatalf("decoded messages = %+v", decoded.Messages)
	}
}

func TestMessageViewsMapDeveloperToSystem(t *testing.T) {
	message := &Message{
		ID:       "msg_dev",
		ThreadID: "th_1",
		Payload:  llm.DeveloperMessage("rules"),
	}
	views := message.Views()
	if len(views) != 1 || views[0].Role != string(llm.RoleSystem) {
		t.Fatalf("developer views = %+v", views)
	}
}
