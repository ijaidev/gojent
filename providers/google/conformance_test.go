package google_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

// TestConformance proves that Generate and Stream produce the same canonical
// response for text, reasoning, and tool calls.
func TestConformance_TextAndReasoning(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w,
					streamCandidate([]map[string]any{{"text": "thinking", "thought": true}}, "", nil),
					streamCandidate([]map[string]any{{"text": "hello "}}, "", nil),
					streamCandidate([]map[string]any{{"text": "world"}}, "STOP", usageBody()),
				)
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"role": "model", "parts": []map[string]any{
						{"text": "thinking", "thought": true},
						{"text": "hello world"},
					}},
					"finishReason": "STOP",
				}},
				"usageMetadata": usageBody(),
			})
		})
	},
		llmtest.Case{
			Name:            "TextAndReasoning",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:            "hello world",
			Reasoning:       "thinking",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "STOP",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

func TestConformance_ToolCalls(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w,
					streamCandidate(toolCallParts()[:1], "", nil),
					streamCandidate(toolCallParts()[1:], "STOP", usageBody()),
				)
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content":      map[string]any{"role": "model", "parts": toolCallParts()},
					"finishReason": "STOP",
				}},
				"usageMetadata": usageBody(),
			})
		})
	},
		llmtest.Case{
			Name:            "ToolCalls",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("use tools")}},
			ToolCalls:       2,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "STOP",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

func TestConformance_ReasoningToolCallText(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	parts := []map[string]any{
		{"text": "considering", "thought": true},
		{"text": "checking the weather"},
		{"functionCall": map[string]any{"id": "call-1", "name": "weather", "args": map[string]any{"city": "Tokyo"}}},
	}
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w,
					streamCandidate(parts[:1], "", nil),
					streamCandidate(parts[1:2], "", nil),
					streamCandidate(parts[2:], "STOP", usageBody()),
				)
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content":      map[string]any{"role": "model", "parts": parts},
					"finishReason": "STOP",
				}},
				"usageMetadata": usageBody(),
			})
		})
	},
		llmtest.Case{
			Name:            "ReasoningToolCallText",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("plan")}},
			Text:            "checking the weather",
			Reasoning:       "considering",
			ToolCalls:       1,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "STOP",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

func TestConformance_ToolErrorRequest(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	transcript := []llm.Message{
		llm.UserMessage("how is the weather?"),
		llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{
			CallID: "call-1",
			Name:   "weather",
			Input:  json.RawMessage(`{"city":"Tokyo"}`),
		})),
		llm.ToolResultsMessage(llm.ToolResult{
			CallID:  "call-1",
			Name:    "weather",
			Status:  llm.ToolStatusError,
			Content: []llm.Part{llm.NewTextPart("upstream timeout")},
		}),
	}

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w,
					streamCandidate([]map[string]any{{"text": "the weather service timed out"}}, "STOP", usageBody()),
				)
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "the weather service timed out"}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": usageBody(),
			})
		})
	},
		llmtest.Case{
			Name:            "ToolErrorRequest",
			Request:         &llm.Request{Messages: transcript},
			Text:            "the weather service timed out",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "STOP",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

func TestConformance_ImageInput(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	transcript := []llm.Message{
		llm.UserMessageWithParts(
			llm.NewTextPart("describe this image"),
			llm.FileFromBytes([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
		),
	}

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w,
					streamCandidate([]map[string]any{{"text": "a tiny image"}}, "STOP", usageBody()),
				)
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "a tiny image"}}},
					"finishReason": "STOP",
				}},
				"usageMetadata": usageBody(),
			})
		})
	},
		llmtest.Case{
			Name:            "ImageInput",
			Request:         &llm.Request{Messages: transcript},
			Text:            "a tiny image",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "STOP",
			Status:          llm.MessageStatusCompleted,
		},
	)
}
