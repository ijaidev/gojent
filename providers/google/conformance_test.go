package google_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

// TestConformance proves that Generate and Stream produce the same canonical
// response for text, reasoning, and tool calls.
func TestConformance_TextAndReasoning(t *testing.T) {
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
			Name:         "TextAndReasoning",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:         "hello world",
			Reasoning:    "thinking",
			FinishReason: llm.FinishReasonStop,
		},
	)
}

func TestConformance_ToolCalls(t *testing.T) {
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
			Name:         "ToolCalls",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("use tools")}},
			ToolCalls:    2,
			FinishReason: llm.FinishReasonTool,
		},
	)
}

func TestConformance_ReasoningToolCallText(t *testing.T) {
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
			Name:         "ReasoningToolCallText",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("plan")}},
			Text:         "checking the weather",
			Reasoning:    "considering",
			ToolCalls:    1,
			FinishReason: llm.FinishReasonTool,
		},
	)
}
