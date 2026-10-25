package llm_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestToolCall_UnmarshalInput(t *testing.T) {
	call := llm.ToolCall{CallID: "c", Name: "lookup", Input: json.RawMessage(`{"q":"go"}`)}
	var input struct {
		Query string `json:"q"`
	}
	if err := call.UnmarshalInput(&input); err != nil {
		t.Fatalf("UnmarshalInput: %v", err)
	}
	if input.Query != "go" {
		t.Errorf("query = %q", input.Query)
	}
	if err := (llm.ToolCall{CallID: "c", Name: "noop"}).UnmarshalInput(&input); err != nil {
		t.Errorf("empty input: %v", err)
	}
	if err := (llm.ToolCall{CallID: "c", Name: "bad", Input: json.RawMessage(`{`)}).UnmarshalInput(&input); err == nil {
		t.Error("invalid input decoded without error")
	}
}

func TestToolCallPart_Validate(t *testing.T) {
	tests := []struct {
		name string
		call llm.ToolCall
		want error
	}{
		{"valid", llm.ToolCall{CallID: "c", Name: "lookup"}, nil},
		{"missing name", llm.ToolCall{CallID: "c"}, llm.ErrInvalidToolCall},
		{"missing call id", llm.ToolCall{Name: "lookup"}, llm.ErrInvalidToolCall},
		{"invalid json", llm.ToolCall{CallID: "c", Name: "lookup", Input: json.RawMessage(`{`)}, llm.ErrInvalidToolCall},
		{"unknown kind", llm.ToolCall{CallID: "c", Name: "lookup", Kind: "remote"}, llm.ErrInvalidToolCall},
		{"unknown status", llm.ToolCall{CallID: "c", Name: "lookup", Status: "pending"}, llm.ErrInvalidToolCall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := llm.AssistantMessageWithParts(llm.NewToolCallPart(tt.call)).Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestToolCallPart_DefaultsKind(t *testing.T) {
	part := llm.NewToolCallPart(llm.ToolCall{CallID: "c", Name: "lookup"})
	if part.Kind != llm.ToolKindFunction {
		t.Errorf("kind = %q, want function", part.Kind)
	}
}

func TestToolResultPart_Defaults(t *testing.T) {
	part := llm.NewToolResultPart(llm.ToolResult{CallID: "c", Content: []llm.Part{llm.NewTextPart("ok")}})
	if part.Kind != llm.ToolKindFunction {
		t.Errorf("kind = %q, want function", part.Kind)
	}
	if part.Status != llm.ToolStatusCompleted {
		t.Errorf("status = %q, want completed", part.Status)
	}
	if part.Text() != "ok" {
		t.Errorf("text = %q", part.Text())
	}
}

func TestToolResultPart_Validate(t *testing.T) {
	tests := []struct {
		name   string
		result llm.ToolResult
		want   error
	}{
		{
			name:   "valid text",
			result: llm.ToolResult{CallID: "c", Content: []llm.Part{llm.NewTextPart("ok")}},
		},
		{
			name:   "valid structured",
			result: llm.ToolResult{CallID: "c", Structured: json.RawMessage(`{"ok":true}`)},
		},
		{
			name:   "missing call id",
			result: llm.ToolResult{Content: []llm.Part{llm.NewTextPart("ok")}},
			want:   llm.ErrInvalidToolResult,
		},
		{
			name:   "invalid structured",
			result: llm.ToolResult{CallID: "c", Structured: json.RawMessage(`{`)},
			want:   llm.ErrInvalidToolResult,
		},
		{
			name: "nested tool call",
			result: llm.ToolResult{
				CallID: "c",
				Content: []llm.Part{
					llm.NewToolCallPart(llm.ToolCall{CallID: "nested", Name: "x"}),
				},
			},
			want: llm.ErrInvalidToolResult,
		},
		{
			name:   "input required without requests",
			result: llm.ToolResult{CallID: "c", Status: llm.ToolStatusInputRequired},
			want:   llm.ErrInvalidToolResult,
		},
		{
			name: "input required",
			result: llm.ToolResult{
				CallID:        "c",
				Status:        llm.ToolStatusInputRequired,
				InputRequests: json.RawMessage(`[{"name":"confirm"}]`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := llm.UserMessageWithParts(llm.NewToolResultPart(tt.result)).Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestToolResultsMessage_GroupsInOrder(t *testing.T) {
	msg := llm.ToolResultsMessage(
		llm.ToolResult{CallID: "a", Content: []llm.Part{llm.NewTextPart("a")}},
		llm.ToolResult{CallID: "b", Content: []llm.Part{llm.NewTextPart("b")}},
	)
	if msg.Role != llm.RoleUser {
		t.Errorf("role = %q", msg.Role)
	}
	results := msg.ToolResults()
	if len(results) != 2 || results[0].CallID != "a" || results[1].CallID != "b" {
		t.Errorf("results = %+v", results)
	}
}
