package anthropic

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

	"github.com/anthropics/anthropic-sdk-go"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// newTestModel starts a fake Messages API and returns a model pointed at it.
func newTestModel(t *testing.T, handler http.HandlerFunc) *Model {
	t.Helper()
	return newTestModelWithConfig(t, Config{}, handler)
}

// newTestModelWithConfig starts a fake Messages API for a caller-supplied
// config. Model, APIKey, and BaseURL are replaced with test values.
func newTestModelWithConfig(t *testing.T, cfg Config, handler http.HandlerFunc) *Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg.Model = "claude-test"
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

// writeSSE writes Anthropic's named-event SSE format.
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

func decodeMessage(t *testing.T, value any) *anthropic.Message {
	t.Helper()
	encoded := mustJSON(t, value)
	var message anthropic.Message
	if err := json.Unmarshal(encoded, &message); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	return &message
}
