package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

// responsesHandler serves the canned non-streaming response unless the request
// asked for a stream, in which case it serves the canned SSE events.
func responsesHandler(t *testing.T, response map[string]any, events ...map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		var probe struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "decode failed", http.StatusBadRequest)
			return
		}
		if probe.Stream {
			writeSSE(t, w, events...)
			return
		}
		writeJSON(t, w, response)
	}
}

// TestConformance_TextReasoningTool proves Generate and Stream agree on a turn
// that interleaves reasoning, text, and a client function call.
func TestConformance_TextReasoningTool(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	reasoning := reasoningItem("rs_1", "considering", "enc_1", "completed")
	message := messageItem("msg_1", "checking the weather")
	call := functionCallItem("fc_1", "call_1", "weather", `{"city":"Tokyo"}`, "completed")
	response := responseFixture([]any{reasoning, message, call}, "completed")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, responsesHandler(t, response,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, reasoningItem("rs_1", "", "", "in_progress")),
			outputItemDoneEvent(2, 0, reasoning),
			outputItemAddedEvent(3, 1, messageItem("msg_1", "")),
			contentPartAddedEvent(4, 1, 0, "msg_1", ""),
			textDeltaEvent(5, 1, 0, "msg_1", "checking the "),
			textDeltaEvent(6, 1, 0, "msg_1", "weather"),
			textDoneEvent(7, 1, 0, "msg_1", "checking the weather"),
			contentPartDoneEvent(8, 1, 0, "msg_1", "checking the weather"),
			outputItemDoneEvent(9, 1, message),
			outputItemAddedEvent(10, 2, functionCallItem("fc_1", "call_1", "weather", "", "in_progress")),
			functionArgsDeltaEvent(11, 2, "fc_1", `{"city":`),
			functionArgsDeltaEvent(12, 2, "fc_1", `"Tokyo"}`),
			functionArgsDoneEvent(13, 2, "fc_1", `{"city":"Tokyo"}`),
			outputItemDoneEvent(14, 2, call),
			responseCompletedEvent(response),
		))
	},
		llmtest.Case{
			Name:            "TextReasoningTool",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("weather?")}},
			Text:            "checking the weather",
			Reasoning:       "considering",
			ToolCalls:       1,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "completed",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_ToolErrorRequest replays a failed function call and checks
// that both paths return the follow-up text.
func TestConformance_ToolErrorRequest(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	transcript := []llm.Message{
		llm.UserMessage("how is the weather?"),
		llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{
			CallID: "call_1",
			Name:   "weather",
			Input:  json.RawMessage(`{"city":"Tokyo"}`),
		})),
		llm.ToolResultsMessage(llm.ToolResult{
			CallID:  "call_1",
			Name:    "weather",
			Status:  llm.ToolStatusError,
			Content: []llm.Part{llm.NewTextPart("upstream timeout")},
		}),
	}
	message := messageItem("msg_1", "the weather service timed out")
	response := responseFixture([]any{message}, "completed")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, responsesHandler(t, response,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
			contentPartAddedEvent(2, 0, 0, "msg_1", ""),
			textDeltaEvent(3, 0, 0, "msg_1", "the weather service timed out"),
			textDoneEvent(4, 0, 0, "msg_1", "the weather service timed out"),
			contentPartDoneEvent(5, 0, 0, "msg_1", "the weather service timed out"),
			outputItemDoneEvent(6, 0, message),
			responseCompletedEvent(response),
		))
	},
		llmtest.Case{
			Name:            "ToolErrorRequest",
			Request:         &llm.Request{Messages: transcript},
			Text:            "the weather service timed out",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "completed",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_HostedSearch checks that a provider-executed item is recorded
// as a server call and never becomes a client tool request.
func TestConformance_HostedSearch(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	search := webSearchItem("ws_1", "go", "completed")
	message := messageItem("msg_1", "found it")
	response := responseFixture([]any{search, message}, "completed")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, responsesHandler(t, response,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, webSearchItem("ws_1", "", "in_progress")),
			outputItemDoneEvent(2, 0, search),
			outputItemAddedEvent(3, 1, messageItem("msg_1", "")),
			contentPartAddedEvent(4, 1, 0, "msg_1", ""),
			textDeltaEvent(5, 1, 0, "msg_1", "found it"),
			textDoneEvent(6, 1, 0, "msg_1", "found it"),
			contentPartDoneEvent(7, 1, 0, "msg_1", "found it"),
			outputItemDoneEvent(8, 1, message),
			responseCompletedEvent(response),
		))
	},
		llmtest.Case{
			Name:            "HostedSearch",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("search for go")}},
			Text:            "found it",
			ToolCalls:       1,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "completed",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_Refusal checks an explicit refusal in both paths.
func TestConformance_Refusal(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	refusal := refusalItem("msg_1", "request blocked")
	response := responseFixture([]any{refusal}, "completed")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, responsesHandler(t, response,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
			map[string]any{
				"type":            "response.content_part.added",
				"sequence_number": 2,
				"item_id":         "msg_1",
				"output_index":    0,
				"content_index":   0,
				"part":            map[string]any{"type": "refusal", "refusal": ""},
			},
			map[string]any{
				"type":            "response.refusal.delta",
				"sequence_number": 3,
				"item_id":         "msg_1",
				"output_index":    0,
				"content_index":   0,
				"delta":           "request blocked",
			},
			map[string]any{
				"type":            "response.refusal.done",
				"sequence_number": 4,
				"item_id":         "msg_1",
				"output_index":    0,
				"content_index":   0,
				"refusal":         "request blocked",
			},
			map[string]any{
				"type":            "response.content_part.done",
				"sequence_number": 5,
				"item_id":         "msg_1",
				"output_index":    0,
				"content_index":   0,
				"part":            map[string]any{"type": "refusal", "refusal": "request blocked"},
			},
			outputItemDoneEvent(6, 0, refusal),
			responseCompletedEvent(response),
		))
	},
		llmtest.Case{
			Name:            "Refusal",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("do something unsafe")}},
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "completed",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_ImageInput checks that an image part is sent and that both
// paths return the same canonical turn.
func TestConformance_ImageInput(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	transcript := []llm.Message{
		llm.UserMessageWithParts(
			llm.NewTextPart("describe this image"),
			llm.FileFromBytes([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
		),
	}
	message := messageItem("msg_1", "a tiny image")
	response := responseFixture([]any{message}, "completed")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, responsesHandler(t, response,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
			contentPartAddedEvent(2, 0, 0, "msg_1", ""),
			textDeltaEvent(3, 0, 0, "msg_1", "a tiny image"),
			textDoneEvent(4, 0, 0, "msg_1", "a tiny image"),
			contentPartDoneEvent(5, 0, 0, "msg_1", "a tiny image"),
			outputItemDoneEvent(6, 0, message),
			responseCompletedEvent(response),
		))
	},
		llmtest.Case{
			Name:            "ImageInput",
			Request:         &llm.Request{Messages: transcript},
			Text:            "a tiny image",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "completed",
			Status:          llm.MessageStatusCompleted,
		},
	)
}
