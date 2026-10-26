package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

func messageStartEvent() map[string]any {
	return map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_1",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-test",
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usageFixture(),
		},
	}
}

func messageDeltaEvent(stopReason string) map[string]any {
	return map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usageFixture(),
	}
}

func TestStreamThinkingAndText(t *testing.T) {
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			messageStartEvent(),
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "thinking", "thinking": "", "signature": "",
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "thinking_delta", "thinking": "considering",
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "signature_delta", "signature": "sig-1",
			}},
			map[string]any{"type": "content_block_stop", "index": 0},
			map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{
				"type": "text", "text": "",
			}},
			map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{
				"type": "text_delta", "text": "hello",
			}},
			map[string]any{"type": "content_block_stop", "index": 1},
			messageDeltaEvent("end_turn"),
			map[string]any{"type": "message_stop"},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	response := llmtest.Collect(t, stream)
	if response.FinishReason != llm.FinishReasonStop || response.RawFinishReason != "end_turn" {
		t.Fatalf("finish = %q raw = %q", response.FinishReason, response.RawFinishReason)
	}
	if response.Status != llm.MessageStatusCompleted {
		t.Fatalf("status = %q", response.Status)
	}
	if len(response.Message.Content) != 2 {
		t.Fatalf("content = %+v", response.Message.Content)
	}

	reasoning, ok := response.Message.Content[0].(*llm.ReasoningPart)
	if !ok {
		t.Fatalf("part 0 = %T", response.Message.Content[0])
	}
	state, err := stateOf(reasoning)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if reasoning.Text != "considering" || state.Signature != "sig-1" {
		t.Fatalf("reasoning = %+v state = %+v", reasoning, state)
	}

	text, ok := response.Message.Content[1].(*llm.TextPart)
	if !ok || text.Text != "hello" {
		t.Fatalf("part 1 = %+v", response.Message.Content[1])
	}

	wantUsage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	if response.Usage != wantUsage {
		t.Fatalf("usage = %+v, want %+v", response.Usage, wantUsage)
	}
}

func TestStreamToolInputDeltas(t *testing.T) {
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			messageStartEvent(),
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "tool_use", "id": "toolu_1", "name": "lookup", "input": map[string]any{},
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": `{"q":`,
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": `"go"}`,
			}},
			map[string]any{"type": "content_block_stop", "index": 0},
			messageDeltaEvent("tool_use"),
			map[string]any{"type": "message_stop"},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("use a tool")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	response := llmtest.Collect(t, stream)
	if response.FinishReason != llm.FinishReasonTool {
		t.Fatalf("finish = %q", response.FinishReason)
	}
	if len(response.Message.Content) != 1 {
		t.Fatalf("content = %+v", response.Message.Content)
	}
	call, ok := response.Message.Content[0].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part = %T", response.Message.Content[0])
	}
	var input map[string]any
	if err := call.UnmarshalInput(&input); err != nil {
		t.Fatalf("decode input %s: %v", call.Input, err)
	}
	if input["q"] != "go" {
		t.Fatalf("input = %s", call.Input)
	}
}

func TestStreamRecordsHostedResult(t *testing.T) {
	hostedResult := map[string]any{
		"type":        "web_search_tool_result",
		"tool_use_id": "srvtoolu_1",
		"content": []any{map[string]any{
			"type":              "web_search_result",
			"title":             "Go",
			"url":               "https://go.dev",
			"encrypted_content": "opaque",
			"page_age":          "1d",
		}},
	}
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			messageStartEvent(),
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search",
				"input": map[string]any{"query": "go"},
			}},
			map[string]any{"type": "content_block_stop", "index": 0},
			map[string]any{"type": "content_block_start", "index": 1, "content_block": hostedResult},
			map[string]any{"type": "content_block_stop", "index": 1},
			map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{
				"type": "text", "text": "",
			}},
			map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{
				"type": "text_delta", "text": "found it",
			}},
			map[string]any{"type": "content_block_stop", "index": 2},
			messageDeltaEvent("end_turn"),
			map[string]any{"type": "message_stop"},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("search")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	response := llmtest.Collect(t, stream)
	if len(response.Message.Content) != 2 {
		t.Fatalf("content = %+v", response.Message.Content)
	}
	call, ok := response.Message.Content[0].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part 0 = %T", response.Message.Content[0])
	}
	if call.EffectiveKind() != llm.ToolKindServer {
		t.Fatalf("call = %+v", call.ToolCall)
	}
	state, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(state.ServerResult, &probe); err != nil {
		t.Fatalf("decode server result: %v", err)
	}
	if probe.Type != "web_search_tool_result" {
		t.Fatalf("server result = %s", state.ServerResult)
	}
	if text, ok := response.Message.Content[1].(*llm.TextPart); !ok || text.Text != "found it" {
		t.Fatalf("part 1 = %+v", response.Message.Content[1])
	}
}

func TestStreamRefusalWithoutContent(t *testing.T) {
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			messageStartEvent(),
			map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": "refusal",
					"stop_details": map[string]any{
						"category":    "general_harms",
						"explanation": "request blocked",
					},
				},
				"usage": usageFixture(),
			},
			map[string]any{"type": "message_stop"},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	response := llmtest.Collect(t, stream)
	if len(response.Message.Content) != 1 {
		t.Fatalf("content = %+v", response.Message.Content)
	}
	refusal, ok := response.Message.Content[0].(*llm.RefusalPart)
	if !ok || refusal.Text != "request blocked" {
		t.Fatalf("part = %+v", response.Message.Content[0])
	}
	if response.FinishReason != llm.FinishReasonContentFilter {
		t.Fatalf("finish = %q", response.FinishReason)
	}
}

func TestStreamErrorEvent(t *testing.T) {
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "overloaded_error",
				"message": "busy",
			},
		})
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("err = %v, want *llm.Error", err)
	}
	if llmErr.Kind != llm.KindOverloaded {
		t.Fatalf("kind = %q", llmErr.Kind)
	}
}
