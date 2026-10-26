package anthropic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ijaidev/gojent/core/llm"
)

func usageFixture() map[string]any {
	return map[string]any{
		"input_tokens":                10,
		"output_tokens":               5,
		"cache_creation_input_tokens": 2,
		"cache_read_input_tokens":     3,
		"output_tokens_details":       map[string]any{"thinking_tokens": 1},
	}
}

func messageFixture(content []any, stopReason string) map[string]any {
	return map[string]any{
		"id":            "msg_1",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-test",
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         usageFixture(),
	}
}

func TestCanonicalResponseThinkingTextAndTool(t *testing.T) {
	message := decodeMessage(t, messageFixture([]any{
		map[string]any{"type": "thinking", "thinking": "considering", "signature": "sig-1"},
		map[string]any{"type": "text", "text": "checking"},
		map[string]any{"type": "tool_use", "id": "toolu_1", "name": "lookup", "input": map[string]any{"q": "go"}},
	}, "tool_use"))

	response, err := (&Model{modelID: "claude-test"}).canonicalResponse(message)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if response.ID != "msg_1" || response.Model != "claude-test" {
		t.Fatalf("response = %+v", response)
	}
	if response.FinishReason != llm.FinishReasonTool || response.Status != llm.MessageStatusCompleted {
		t.Fatalf("finish = %q status = %q", response.FinishReason, response.Status)
	}
	if len(response.Message.Content) != 3 {
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

	call, ok := response.Message.Content[2].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part 2 = %T", response.Message.Content[2])
	}
	if call.CallID != "toolu_1" || call.Name != "lookup" || string(call.Input) != `{"q":"go"}` {
		t.Fatalf("call = %+v", call.ToolCall)
	}

	wantUsage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	if response.Usage != wantUsage {
		t.Fatalf("usage = %+v, want %+v", response.Usage, wantUsage)
	}
	if len(response.Message.Content) > 0 {
		encoded := mustJSON(t, response)
		assertGolden(t, "response.golden.json", indentJSON(t, encoded))
	}
}

func TestCanonicalResponseRecordsHostedResult(t *testing.T) {
	message := decodeMessage(t, messageFixture([]any{
		map[string]any{
			"type":  "server_tool_use",
			"id":    "srvtoolu_1",
			"name":  "web_search",
			"input": map[string]any{"query": "go"},
		},
		map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": "srvtoolu_1",
			"content": []any{map[string]any{
				"type":              "web_search_result",
				"title":             "Go",
				"url":               "https://go.dev",
				"encrypted_content": "opaque",
				"page_age":          "1d",
			}},
		},
	}, "end_turn"))

	response, err := (&Model{modelID: "claude-test"}).canonicalResponse(message)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if len(response.Message.Content) != 1 {
		t.Fatalf("content = %+v, want one server tool call", response.Message.Content)
	}
	call, ok := response.Message.Content[0].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part = %T", response.Message.Content[0])
	}
	if call.EffectiveKind() != llm.ToolKindServer || call.Name != "web_search" {
		t.Fatalf("call = %+v", call.ToolCall)
	}
	if got := response.Message.ToolResults(); len(got) != 0 {
		t.Fatalf("server results must not become tool results: %+v", got)
	}

	state, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if len(state.ServerResult) == 0 {
		t.Fatal("server result was not recorded")
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(state.ServerResult, &probe); err != nil {
		t.Fatalf("decode recorded result: %v", err)
	}
	if probe.Type != "web_search_tool_result" {
		t.Fatalf("recorded type = %q", probe.Type)
	}

	blocks, err := convertAssistantContent(response.Message.Content)
	if err != nil {
		t.Fatalf("convertAssistantContent: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want server call and recorded result", len(blocks))
	}
	if blocks[0].OfServerToolUse == nil || blocks[0].OfServerToolUse.ID != "srvtoolu_1" {
		t.Fatalf("server call block = %+v", blocks[0])
	}
	if blocks[1].OfWebSearchToolResult == nil {
		t.Fatalf("recorded result block = %+v", blocks[1])
	}
}

func TestCanonicalResponseRefusalWithoutContent(t *testing.T) {
	fixture := messageFixture(nil, "refusal")
	fixture["stop_details"] = map[string]any{
		"category":    "general_harms",
		"explanation": "request blocked",
	}
	message := decodeMessage(t, fixture)

	response, err := (&Model{modelID: "claude-test"}).canonicalResponse(message)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if len(response.Message.Content) != 1 {
		t.Fatalf("content = %+v", response.Message.Content)
	}
	refusal, ok := response.Message.Content[0].(*llm.RefusalPart)
	if !ok {
		t.Fatalf("part = %T", response.Message.Content[0])
	}
	if refusal.Text != "request blocked" {
		t.Fatalf("refusal = %q", refusal.Text)
	}
	if response.FinishReason != llm.FinishReasonContentFilter {
		t.Fatalf("finish = %q", response.FinishReason)
	}
}

func TestCanonicalResponseRejectsUnknownBlock(t *testing.T) {
	message := decodeMessage(t, messageFixture([]any{
		map[string]any{"type": "future_block", "text": "?"},
	}, "end_turn"))
	if _, err := (&Model{modelID: "claude-test"}).canonicalResponse(message); !errors.Is(err, llm.ErrUnsupportedPartType) {
		t.Fatalf("err = %v, want ErrUnsupportedPartType", err)
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		reason anthropic.StopReason
		finish llm.FinishReason
		status llm.MessageStatus
	}{
		{anthropic.StopReasonEndTurn, llm.FinishReasonStop, llm.MessageStatusCompleted},
		{anthropic.StopReasonStopSequence, llm.FinishReasonStop, llm.MessageStatusCompleted},
		{anthropic.StopReasonMaxTokens, llm.FinishReasonLength, llm.MessageStatusIncomplete},
		{anthropic.StopReasonModelContextWindowExceeded, llm.FinishReasonLength, llm.MessageStatusIncomplete},
		{anthropic.StopReasonToolUse, llm.FinishReasonTool, llm.MessageStatusCompleted},
		{anthropic.StopReasonRefusal, llm.FinishReasonContentFilter, llm.MessageStatusIncomplete},
		{anthropic.StopReasonPauseTurn, llm.FinishReasonStop, llm.MessageStatusIncomplete},
	}
	for _, test := range tests {
		finish, status := mapStopReason(test.reason)
		if finish != test.finish || status != test.status {
			t.Errorf("%q: got %q/%q, want %q/%q", test.reason, finish, status, test.finish, test.status)
		}
	}
}

func TestUsageFromValuesSumsCacheTokens(t *testing.T) {
	usage := usageFromValues(10, 5, 2, 3, 1)
	if usage.PromptTokens != 15 || usage.TotalTokens != 20 || usage.CachedTokens != 3 || usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestHostedResultRejectsUnknownType(t *testing.T) {
	_, err := hostedResultBlock(json.RawMessage(`{"type":"text","text":"not a result"}`), "web_search")
	if !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("err = %v, want ErrUnsupportedPart", err)
	}
}

func TestReasoningWithoutSignatureFailsClosed(t *testing.T) {
	message := decodeMessage(t, messageFixture([]any{
		map[string]any{"type": "thinking", "thinking": "unsigned", "signature": ""},
	}, "end_turn"))
	if _, err := (&Model{modelID: "claude-test"}).canonicalResponse(message); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("err = %v, want ErrUnsupportedPart", err)
	}
}

func TestToolResultStructuredOutputIsPreserved(t *testing.T) {
	result := &llm.ToolResult{
		CallID:     "call-1",
		Name:       "lookup",
		Kind:       llm.ToolKindFunction,
		Status:     llm.ToolStatusCompleted,
		Content:    []llm.Part{llm.NewTextPart("42")},
		Structured: json.RawMessage(`{"answer":42}`),
	}
	block, err := convertToolResultBlock(result)
	if err != nil {
		t.Fatalf("convertToolResultBlock: %v", err)
	}
	if block.OfToolResult == nil || len(block.OfToolResult.Content) != 2 {
		t.Fatalf("block = %+v", block.OfToolResult)
	}
	var texts []string
	for _, content := range block.OfToolResult.Content {
		if content.OfText != nil {
			texts = append(texts, content.OfText.Text)
		}
	}
	if !strings.Contains(strings.Join(texts, " "), `{"answer":42}`) {
		t.Fatalf("structured output missing: %v", texts)
	}
}
