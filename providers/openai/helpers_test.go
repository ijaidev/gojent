package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// newTestModel starts a fake Responses API and returns a model pointed at it.
func newTestModel(t *testing.T, handler http.HandlerFunc) *Model {
	t.Helper()
	return newTestModelWithConfig(t, Config{}, handler)
}

// newTestModelWithConfig starts a fake Responses API for a caller-supplied
// config. Model, APIKey, and BaseURL are replaced with test values.
func newTestModelWithConfig(t *testing.T, cfg Config, handler http.HandlerFunc) *Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg.Model = "gpt-test"
	cfg.APIKey = "test-key"
	cfg.BaseURL = server.URL
	model, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// writeSSE writes Responses API named-event SSE frames.
func writeSSE(t *testing.T, w http.ResponseWriter, events ...map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		eventType, _ := event["type"].(string)
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Errorf("encode event: %v", err)
			return
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded); err != nil {
			t.Errorf("write event: %v", err)
			return
		}
		flusher.Flush()
	}
}

// assertGolden compares JSON against a file in testdata, or rewrites it with
// -update.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run go test -update): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func indentJSON(t *testing.T, encoded []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return append(pretty, '\n')
}

func usageFixture() map[string]any {
	return map[string]any{
		"input_tokens":         10,
		"input_tokens_details": map[string]any{"cached_tokens": 3},
		"output_tokens":        5,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": 1,
		},
		"total_tokens": 15,
	}
}

// responseFixture builds the terminal Response object the SDK decodes.
func responseFixture(output []any, status string) map[string]any {
	if output == nil {
		output = []any{}
	}
	return map[string]any{
		"id":                  "resp_1",
		"object":              "response",
		"created_at":          1700000000,
		"model":               "gpt-test",
		"status":              status,
		"output":              output,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
		"usage":               usageFixture(),
	}
}

func responseCreatedEvent(response map[string]any) map[string]any {
	return map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response":        response,
	}
}

func responseCompletedEvent(response map[string]any) map[string]any {
	return map[string]any{
		"type":            "response.completed",
		"sequence_number": 100,
		"response":        response,
	}
}

func messageItem(id, text string) map[string]any {
	content := []any{}
	if text != "" {
		content = append(content, map[string]any{
			"type":        "output_text",
			"text":        text,
			"annotations": []any{},
		})
	}
	return map[string]any{
		"id":      id,
		"type":    "message",
		"role":    "assistant",
		"status":  "completed",
		"content": content,
	}
}

func refusalItem(id, text string) map[string]any {
	return map[string]any{
		"id":     id,
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []any{
			map[string]any{"type": "refusal", "refusal": text},
		},
	}
}

func reasoningItem(id, summary, encrypted, status string) map[string]any {
	summaries := []any{}
	if summary != "" {
		summaries = append(summaries, map[string]any{
			"type": "summary_text",
			"text": summary,
		})
	}
	return map[string]any{
		"id":                id,
		"type":              "reasoning",
		"status":            status,
		"summary":           summaries,
		"encrypted_content": encrypted,
	}
}

func functionCallItem(id, callID, name, arguments, status string) map[string]any {
	return map[string]any{
		"id":        id,
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
		"status":    status,
	}
}

func webSearchItem(id, query, status string) map[string]any {
	return map[string]any{
		"id":     id,
		"type":   "web_search_call",
		"status": status,
		"action": map[string]any{"type": "search", "query": query},
	}
}

func outputItemAddedEvent(sequence int, outputIndex int64, item map[string]any) map[string]any {
	return map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": sequence,
		"output_index":    outputIndex,
		"item":            item,
	}
}

func outputItemDoneEvent(sequence int, outputIndex int64, item map[string]any) map[string]any {
	return map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": sequence,
		"output_index":    outputIndex,
		"item":            item,
	}
}

func contentPartAddedEvent(sequence int, outputIndex, contentIndex int64, itemID, text string) map[string]any {
	return map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"part":            map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}
}

func contentPartDoneEvent(sequence int, outputIndex, contentIndex int64, itemID, text string) map[string]any {
	return map[string]any{
		"type":            "response.content_part.done",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"part":            map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}
}

func textDeltaEvent(sequence int, outputIndex, contentIndex int64, itemID, delta string) map[string]any {
	return map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"delta":           delta,
		"logprobs":        []any{},
	}
}

func textDoneEvent(sequence int, outputIndex, contentIndex int64, itemID, text string) map[string]any {
	return map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"text":            text,
		"logprobs":        []any{},
	}
}

func functionArgsDeltaEvent(sequence int, outputIndex int64, itemID, delta string) map[string]any {
	return map[string]any{
		"type":            "response.function_call_arguments.delta",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"delta":           delta,
	}
}

func functionArgsDoneEvent(sequence int, outputIndex int64, itemID, arguments string) map[string]any {
	return map[string]any{
		"type":            "response.function_call_arguments.done",
		"sequence_number": sequence,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"arguments":       arguments,
	}
}
