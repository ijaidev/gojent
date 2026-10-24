package google_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
)

func TestConformance_TextAndThinking(t *testing.T) {
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w, []map[string]any{
					streamCandidate([]map[string]any{{"text": "thinking", "thought": true}}, "", nil),
					streamCandidate([]map[string]any{{"text": "hello "}}, "", nil),
					streamCandidate([]map[string]any{{"text": "world"}}, "STOP", conformanceUsage()),
				})
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
				"usageMetadata": conformanceUsage(),
			})
		})
	},
		llmtest.Case{
			Name:         "TextAndThinking",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:         "hello world",
			Thinking:     "thinking",
			FinishReason: llm.FinishReasonStop,
		},
	)
}

func TestConformance_ToolCalls(t *testing.T) {
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newModel(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, ":streamGenerateContent") {
				writeSSE(t, w, []map[string]any{
					streamCandidate(toolParts()[:1], "", nil),
					streamCandidate(toolParts()[1:], "STOP", conformanceUsage()),
				})
				return
			}
			writeJSON(t, w, map[string]any{
				"candidates": []map[string]any{{
					"content":      map[string]any{"role": "model", "parts": toolParts()},
					"finishReason": "STOP",
				}},
				"usageMetadata": conformanceUsage(),
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

func conformanceUsage() map[string]any {
	return map[string]any{
		"promptTokenCount":        3,
		"candidatesTokenCount":    5,
		"totalTokenCount":         8,
		"thoughtsTokenCount":      2,
		"cachedContentTokenCount": 1,
	}
}

func toolParts() []map[string]any {
	return []map[string]any{
		{"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{"q": "go"}}},
		{"functionCall": map[string]any{"id": "call-2", "name": "clock", "args": map[string]any{}}},
	}
}

func streamCandidate(parts []map[string]any, finish string, usage map[string]any) map[string]any {
	candidate := map[string]any{
		"content": map[string]any{"role": "model", "parts": parts},
	}
	if finish != "" {
		candidate["finishReason"] = finish
	}
	chunk := map[string]any{"candidates": []map[string]any{candidate}}
	if usage != nil {
		chunk["usageMetadata"] = usage
	}
	return chunk
}

func writeSSE(t *testing.T, w http.ResponseWriter, chunks []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for _, chunk := range chunks {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			t.Errorf("encode chunk: %v", err)
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
			t.Errorf("write chunk: %v", err)
			return
		}
		flusher.Flush()
	}
}
