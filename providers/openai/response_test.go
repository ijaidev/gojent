package openai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/responses"
)

// decodeResponse decodes a JSON fixture through the pinned SDK so tests
// exercise the same types the adapter receives from the API.
func decodeResponse(t *testing.T, value map[string]any) *responses.Response {
	t.Helper()
	encoded := mustJSON(t, value)
	var response responses.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &response
}

func TestCanonicalResponseReasoningTextAndCall(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		reasoningItem("rs_1", "thinking hard", "encrypted-1", "completed"),
		messageItem("msg_1", "checking"),
		functionCallItem("fc_1", "call_1", "lookup", `{"q":"go"}`, "completed"),
	}, "completed"))

	canonical, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if canonical.ID != "resp_1" || canonical.Model != "gpt-test" {
		t.Fatalf("response = %+v", canonical)
	}
	if canonical.FinishReason != llm.FinishReasonTool || canonical.Status != llm.MessageStatusCompleted {
		t.Fatalf("finish = %q status = %q", canonical.FinishReason, canonical.Status)
	}
	if canonical.RawFinishReason != "completed" {
		t.Fatalf("raw finish = %q", canonical.RawFinishReason)
	}
	wantUsage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	if canonical.Usage != wantUsage {
		t.Fatalf("usage = %+v, want %+v", canonical.Usage, wantUsage)
	}
	if len(canonical.Message.Content) != 3 {
		t.Fatalf("content = %+v", canonical.Message.Content)
	}

	reasoning, ok := canonical.Message.Content[0].(*llm.ReasoningPart)
	if !ok {
		t.Fatalf("part 0 = %T", canonical.Message.Content[0])
	}
	if len(reasoning.Summary) != 1 || reasoning.Summary[0] != "thinking hard" {
		t.Fatalf("reasoning = %+v", reasoning)
	}
	state, err := stateOf(reasoning)
	if err != nil {
		t.Fatalf("stateOf reasoning: %v", err)
	}
	if state.ItemID != "rs_1" || state.EncryptedContent != "encrypted-1" || state.Status != "completed" {
		t.Fatalf("reasoning state = %+v", state)
	}

	text, ok := canonical.Message.Content[1].(*llm.TextPart)
	if !ok || text.Text != "checking" {
		t.Fatalf("part 1 = %+v", canonical.Message.Content[1])
	}

	call, ok := canonical.Message.Content[2].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part 2 = %T", canonical.Message.Content[2])
	}
	if call.ItemID != "fc_1" || call.CallID != "call_1" || call.Name != "lookup" {
		t.Fatalf("call = %+v", call.ToolCall)
	}
	if string(call.Input) != `{"q":"go"}` {
		t.Fatalf("input = %s", call.Input)
	}
	if call.Status != "" {
		t.Fatalf("canonical call status = %q, want empty", call.Status)
	}
	callState, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf call: %v", err)
	}
	if callState.Status != "completed" {
		t.Fatalf("call state = %+v", callState)
	}

	assertGolden(t, "response.golden.json", indentJSON(t, mustJSON(t, canonical)))
}

func TestCanonicalResponseRecordsHostedItem(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		webSearchItem("ws_1", "go", "completed"),
		messageItem("msg_1", "found it"),
	}, "completed"))

	canonical, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if canonical.FinishReason != llm.FinishReasonStop {
		t.Fatalf("finish = %q, want stop because hosted calls are not client work", canonical.FinishReason)
	}
	if got := canonical.Message.ToolResults(); len(got) != 0 {
		t.Fatalf("hosted results must not become tool results: %+v", got)
	}

	call, ok := canonical.Message.Content[0].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part 0 = %T", canonical.Message.Content[0])
	}
	if call.EffectiveKind() != llm.ToolKindServer || call.Name != "web_search" {
		t.Fatalf("call = %+v", call.ToolCall)
	}
	if call.ItemID != "ws_1" || call.CallID != "ws_1" {
		t.Fatalf("call identity = %+v", call.ToolCall)
	}
	state, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if len(state.ServerItem) == 0 {
		t.Fatal("hosted item was not recorded")
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(state.ServerItem, &probe); err != nil {
		t.Fatalf("decode recorded item: %v", err)
	}
	if probe.Type != "web_search_call" {
		t.Fatalf("recorded type = %q", probe.Type)
	}
}

func TestCanonicalResponseIncomplete(t *testing.T) {
	tests := []struct {
		reason string
		finish llm.FinishReason
		raw    string
	}{
		{reason: "max_output_tokens", finish: llm.FinishReasonLength, raw: "max_output_tokens"},
		{reason: "content_filter", finish: llm.FinishReasonContentFilter, raw: "content_filter"},
		{reason: "", finish: llm.FinishReasonStop, raw: "incomplete"},
		{reason: "future_reason", finish: llm.FinishReason("future_reason"), raw: "future_reason"},
	}
	for _, test := range tests {
		fixture := responseFixture([]any{messageItem("msg_1", "partial")}, "incomplete")
		if test.reason != "" {
			fixture["incomplete_details"] = map[string]any{"reason": test.reason}
		}
		response := decodeResponse(t, fixture)

		canonical, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
		if err != nil {
			t.Fatalf("%q: canonicalResponse: %v", test.reason, err)
		}
		if canonical.Status != llm.MessageStatusIncomplete {
			t.Errorf("%q: status = %q", test.reason, canonical.Status)
		}
		if canonical.FinishReason != test.finish {
			t.Errorf("%q: finish = %q, want %q", test.reason, canonical.FinishReason, test.finish)
		}
		if canonical.RawFinishReason != test.raw {
			t.Errorf("%q: raw = %q, want %q", test.reason, canonical.RawFinishReason, test.raw)
		}
	}
}

func TestCanonicalResponseFailedAndCancelled(t *testing.T) {
	tests := []struct {
		name   string
		status string
		code   string
		kind   llm.Kind
	}{
		{name: "rate limit", status: "failed", code: "rate_limit_exceeded", kind: llm.KindRateLimit},
		{name: "server", status: "failed", code: "server_error", kind: llm.KindProvider},
		{name: "no code", status: "failed", kind: llm.KindProvider},
		{name: "canceled", status: "canceled", kind: llm.KindProvider},
		{name: "non-terminal", status: "in_progress", kind: llm.KindProvider},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := responseFixture([]any{messageItem("msg_1", "ignored")}, test.status)
			if test.code != "" {
				fixture["error"] = map[string]any{"code": test.code, "message": "boom"}
			}
			response := decodeResponse(t, fixture)

			_, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
			var llmErr *llm.Error
			if !errors.As(err, &llmErr) {
				t.Fatalf("err = %v, want *llm.Error", err)
			}
			if llmErr.Kind != test.kind {
				t.Fatalf("kind = %q, want %q", llmErr.Kind, test.kind)
			}
		})
	}
}

func TestCanonicalResponseRefusal(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		refusalItem("msg_1", "request blocked"),
	}, "completed"))

	canonical, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if err != nil {
		t.Fatalf("canonicalResponse: %v", err)
	}
	if len(canonical.Message.Content) != 1 {
		t.Fatalf("content = %+v", canonical.Message.Content)
	}
	refusal, ok := canonical.Message.Content[0].(*llm.RefusalPart)
	if !ok || refusal.Text != "request blocked" {
		t.Fatalf("part = %+v", canonical.Message.Content[0])
	}
	if canonical.FinishReason != llm.FinishReasonStop {
		t.Fatalf("finish = %q", canonical.FinishReason)
	}
}

func TestCanonicalResponseRejectsUnknownOutputItem(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		map[string]any{"id": "future_1", "type": "future_call"},
	}, "completed"))

	_, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if !errors.Is(err, llm.ErrUnsupportedPartType) {
		t.Fatalf("err = %v, want ErrUnsupportedPartType", err)
	}
}

func TestCanonicalResponseRejectsEmptyContent(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		messageItem("msg_1", ""),
	}, "completed"))

	_, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("err = %v, want ErrInvalidMessage", err)
	}
}

func TestFunctionCallPartRequiresIdentity(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
	}{
		{
			name: "missing call id",
			item: map[string]any{"id": "fc_1", "type": "function_call", "name": "lookup", "arguments": "{}"},
		},
		{
			name: "missing name",
			item: map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_1", "arguments": "{}"},
		},
		{
			name: "invalid arguments",
			item: map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": "not json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := decodeResponse(t, responseFixture([]any{test.item}, "completed"))
			_, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
			if err == nil {
				t.Fatal("canonicalResponse succeeded, want an error")
			}
		})
	}
}

func TestReasoningPartRequiresItemID(t *testing.T) {
	response := decodeResponse(t, responseFixture([]any{
		reasoningItem("", "summary", "", "completed"),
	}, "completed"))

	_, err := (&Model{modelID: "gpt-test"}).canonicalResponse(response)
	if !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("err = %v, want ErrUnsupportedPart", err)
	}
}

func TestHostedToolName(t *testing.T) {
	tests := map[string]string{
		"web_search_call":       "web_search",
		"file_search_call":      "file_search",
		"computer_call":         "computer_use",
		"code_interpreter_call": "code_interpreter",
		"image_generation_call": "image_generation",
		"local_shell_call":      "local_shell",
		"mcp_call":              "mcp_call",
	}
	for itemType, want := range tests {
		item := &responses.ResponseOutputItemUnion{Type: itemType}
		if got := hostedToolName(item); got != want {
			t.Errorf("%s: name = %q, want %q", itemType, got, want)
		}
	}
}

func TestRawFinishReason(t *testing.T) {
	completed := &responses.Response{Status: responses.ResponseStatusCompleted}
	if got := rawFinishReason(completed); got != "completed" {
		t.Fatalf("completed raw = %q", got)
	}
	incomplete := &responses.Response{
		Status:            responses.ResponseStatusIncomplete,
		IncompleteDetails: responses.ResponseIncompleteDetails{Reason: "max_output_tokens"},
	}
	if got := rawFinishReason(incomplete); got != "max_output_tokens" {
		t.Fatalf("incomplete raw = %q", got)
	}
}
