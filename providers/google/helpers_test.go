package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/google"
	"google.golang.org/genai"
)

// newModel starts a test server and returns a model pointed at it.
func newModel(t *testing.T, handler http.HandlerFunc) *google.Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m, err := google.New(context.Background(), google.Config{
		Model:  "gemini-test",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// newModelWithConfig starts a test server for a caller-supplied config.
func newModelWithConfig(t *testing.T, cfg google.Config, handler http.HandlerFunc) *google.Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg.Model = "gemini-test"
	cfg.APIKey = "test-key"
	cfg.Client = nil
	cfg.ClientConfig = &genai.ClientConfig{
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	}
	m, err := google.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// writeSSE writes Gemini's JSON-per-event streaming format.
func writeSSE(t *testing.T, w http.ResponseWriter, chunks ...map[string]any) {
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

func readBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return decoded
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

func usageBody() map[string]any {
	return map[string]any{
		"promptTokenCount":        3,
		"candidatesTokenCount":    5,
		"totalTokenCount":         8,
		"thoughtsTokenCount":      2,
		"cachedContentTokenCount": 1,
	}
}

func toolCallParts() []map[string]any {
	return []map[string]any{
		{"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{"q": "go"}}},
		{"functionCall": map[string]any{"id": "call-2", "name": "clock", "args": map[string]any{}}},
	}
}

// providerSignature returns the Google thought signature stored on a part.
func providerSignature(t *testing.T, part llm.Part) []byte {
	t.Helper()
	var state struct {
		ThoughtSignature []byte `json:"thought_signature"`
	}
	if err := part.Meta().Get("google", &state); err != nil {
		return nil
	}
	return state.ThoughtSignature
}

// collectStream drains a stream into canonical events.
func collectStream(t *testing.T, stream llm.Stream) []llm.StreamEvent {
	t.Helper()
	defer func() {
		if err := stream.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	var events []llm.StreamEvent
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		events = append(events, event)
	}
	return events
}
