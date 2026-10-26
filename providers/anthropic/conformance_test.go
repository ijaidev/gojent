package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

// messagesHandler serves the canned non-streaming response unless the request
// asked for a stream, in which case it serves the canned SSE events.
func messagesHandler(t *testing.T, response map[string]any, events ...map[string]any) http.HandlerFunc {
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
// that interleaves thinking, text, and a client tool call.
func TestConformance_TextReasoningTool(t *testing.T) {
	usage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	response := messageFixture([]any{
		map[string]any{"type": "thinking", "thinking": "considering", "signature": "sig-1"},
		map[string]any{"type": "text", "text": "checking the weather"},
		map[string]any{"type": "tool_use", "id": "toolu_1", "name": "weather", "input": map[string]any{"city": "Tokyo"}},
	}, "tool_use")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, messagesHandler(t, response,
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
				"type": "text_delta", "text": "checking the weather",
			}},
			map[string]any{"type": "content_block_stop", "index": 1},
			map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{
				"type": "tool_use", "id": "toolu_1", "name": "weather", "input": map[string]any{},
			}},
			map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": `{"city":`,
			}},
			map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": `"Tokyo"}`,
			}},
			map[string]any{"type": "content_block_stop", "index": 2},
			messageDeltaEvent("tool_use"),
			map[string]any{"type": "message_stop"},
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
			RawFinishReason: "tool_use",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_ToolErrorRequest replays a failed function call and checks
// that both paths return the follow-up text.
func TestConformance_ToolErrorRequest(t *testing.T) {
	usage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	transcript := []llm.Message{
		llm.UserMessage("how is the weather?"),
		llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{
			CallID: "toolu_1",
			Name:   "weather",
			Input:  json.RawMessage(`{"city":"Tokyo"}`),
		})),
		llm.ToolResultsMessage(llm.ToolResult{
			CallID:  "toolu_1",
			Name:    "weather",
			Status:  llm.ToolStatusError,
			Content: []llm.Part{llm.NewTextPart("upstream timeout")},
		}),
	}
	response := messageFixture([]any{
		map[string]any{"type": "text", "text": "the weather service timed out"},
	}, "end_turn")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, messagesHandler(t, response,
			messageStartEvent(),
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "text", "text": "",
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "text_delta", "text": "the weather service timed out",
			}},
			map[string]any{"type": "content_block_stop", "index": 0},
			messageDeltaEvent("end_turn"),
			map[string]any{"type": "message_stop"},
		))
	},
		llmtest.Case{
			Name:            "ToolErrorRequest",
			Request:         &llm.Request{Messages: transcript},
			Text:            "the weather service timed out",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "end_turn",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_HostedSearch checks that a provider-executed tool keeps its
// recorded result on the server call and produces the same canonical turn in
// both paths.
func TestConformance_HostedSearch(t *testing.T) {
	usage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
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
	response := messageFixture([]any{
		map[string]any{
			"type":  "server_tool_use",
			"id":    "srvtoolu_1",
			"name":  "web_search",
			"input": map[string]any{"query": "go"},
		},
		hostedResult,
		map[string]any{"type": "text", "text": "found it"},
	}, "end_turn")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, messagesHandler(t, response,
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
		))
	},
		llmtest.Case{
			Name:            "HostedSearch",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("search for go")}},
			Text:            "found it",
			ToolCalls:       1,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "end_turn",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestConformance_Refusal checks a classifier refusal in both paths.
func TestConformance_Refusal(t *testing.T) {
	usage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	response := messageFixture(nil, "refusal")
	response["stop_details"] = map[string]any{
		"category":    "general_harms",
		"explanation": "request blocked",
	}

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, messagesHandler(t, response,
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
		))
	},
		llmtest.Case{
			Name:            "Refusal",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("do something unsafe")}},
			Usage:           &usage,
			FinishReason:    llm.FinishReasonContentFilter,
			RawFinishReason: "refusal",
			Status:          llm.MessageStatusIncomplete,
		},
	)
}

// TestConformance_ImageInput checks that an image part is sent and that both
// paths return the same canonical turn.
func TestConformance_ImageInput(t *testing.T) {
	usage := llm.Usage{PromptTokens: 15, CompletionTokens: 5, TotalTokens: 20, ReasoningTokens: 1, CachedTokens: 3}
	transcript := []llm.Message{
		llm.UserMessageWithParts(
			llm.NewTextPart("describe this image"),
			llm.FileFromBytes([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
		),
	}
	response := messageFixture([]any{
		map[string]any{"type": "text", "text": "a tiny image"},
	}, "end_turn")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestModel(t, messagesHandler(t, response,
			messageStartEvent(),
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "text", "text": "",
			}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "text_delta", "text": "a tiny image",
			}},
			map[string]any{"type": "content_block_stop", "index": 0},
			messageDeltaEvent("end_turn"),
			map[string]any{"type": "message_stop"},
		))
	},
		llmtest.Case{
			Name:            "ImageInput",
			Request:         &llm.Request{Messages: transcript},
			Text:            "a tiny image",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "end_turn",
			Status:          llm.MessageStatusCompleted,
		},
	)
}
