package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/google"
	"google.golang.org/genai"
)

// Ensure Model implements the core llm.LLM interface.
var _ llm.LLM = (*google.Model)(nil)

func TestModel_ConfigValidation(t *testing.T) {
	ctx := context.Background()

	// Model is required; empty model must return ErrModelNotSet
	_, err := google.New(ctx, google.Config{
		APIKey: "test-api-key",
	})
	if !errors.Is(err, google.ErrModelNotSet) {
		t.Fatalf("expected ErrModelNotSet when model is empty, got %v", err)
	}

	// Custom model
	customModel, err := google.NewModel(ctx, google.Config{
		Model:  "gemini-1.5-pro",
		APIKey: "test-api-key",
	})
	if err != nil {
		t.Fatalf("unexpected error creating custom model: %v", err)
	}

	if customModel.ModelID() != "gemini-1.5-pro" {
		t.Errorf("expected custom model gemini-1.5-pro, got %s", customModel.ModelID())
	}
}

func TestModel_APIKeyFromEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-gemini-key")

	ctx := context.Background()
	m, err := google.New(ctx, google.Config{
		Model: "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("unexpected error creating model from env: %v", err)
	}

	if m.ModelID() != "gemini-2.5-flash" {
		t.Errorf("expected gemini-2.5-flash, got %s", m.ModelID())
	}
}

func TestModel_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel context

	m, err := google.New(context.Background(), google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = m.Generate(ctx, llm.GenerateRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Generate, got %v", err)
	}

	_, err = m.Stream(ctx, llm.GenerateRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Stream, got %v", err)
	}
}

func TestModel_Generate_EndToEndMockServer(t *testing.T) {
	// Create a mock Gemini API HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{"text": "Gemini response from Gojent!"},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     15,
				"candidatesTokenCount": 25,
				"totalTokenCount":      40,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := context.Background()
	m, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: ts.URL,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	temp := 0.7
	maxTokens := 100
	req := llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a helpful assistant."},
			{Role: llm.RoleUser, Content: "Hello Gemini!"},
		},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}

	res, err := m.Generate(ctx, req)
	if err != nil {
		t.Fatalf("m.Generate failed: %v", err)
	}

	if res.Text != "Gemini response from Gojent!" {
		t.Errorf("unexpected response text: %s", res.Text)
	}
	if res.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected finish reason stop, got %s", res.FinishReason)
	}
	if res.Usage.TotalTokens != 40 {
		t.Errorf("expected 40 total tokens, got %d", res.Usage.TotalTokens)
	}
	if res.Usage.PromptTokens != 15 || res.Usage.CompletionTokens != 25 {
		t.Errorf("unexpected token breakdown: %+v", res.Usage)
	}
}

func TestModel_Stream_EndToEndMockServer(t *testing.T) {
	// Create a mock server that simulates Gemini server-sent stream
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The Gemini SDK expects a JSON array or chunked JSON objects
		// In REST mode with SSE or streamGenerateContent, chunks are formatted as SSE: "data: {...}\n\n"
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		chunk1 := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": "Chunk 1 "},
						},
					},
				},
			},
		}
		chunk2 := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": "Chunk 2"},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     5,
				"candidatesTokenCount": 10,
				"totalTokenCount":      15,
			},
		}

		c1Bytes, _ := json.Marshal(chunk1)
		c2Bytes, _ := json.Marshal(chunk2)

		_, _ = fmt.Fprintf(w, "data: %s\n\n", c1Bytes)
		flusher.Flush()

		_, _ = fmt.Fprintf(w, "data: %s\n\n", c2Bytes)
		flusher.Flush()
	}))
	defer ts.Close()

	ctx := context.Background()
	m, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: ts.URL,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	stream, err := m.Stream(ctx, llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "stream this"},
		},
	})
	if err != nil {
		t.Fatalf("m.Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var accumulated strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		accumulated.WriteString(chunk.Delta)
	}

	if accumulated.String() != "Chunk 1 Chunk 2" {
		t.Errorf("expected 'Chunk 1 Chunk 2', got '%s'", accumulated.String())
	}
}

func TestModel_Generate_FunctionCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{
								"functionCall": map[string]any{
									"name": "get_weather",
									"args": map[string]any{
										"location": "San Francisco",
									},
								},
							},
						},
					},
					"finishReason": "STOP",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := context.Background()
	m, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: ts.URL,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := llm.GenerateRequest{
		Messages: []llm.Message{
			llm.UserMessage("What is the weather in SF?"),
			llm.ToolResultMessage("call-1", `{"temp":20}`),
		},
		Tools: []llm.ToolDefinition{
			{Name: "get_weather", Description: "Fetches weather"},
		},
	}

	res, err := m.Generate(ctx, req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if res.FinishReason != llm.FinishReasonTool {
		t.Errorf("expected finish reason tool_calls, got %s", res.FinishReason)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Name != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %s", res.ToolCalls[0].Name)
	}

	var parsedArgs map[string]any
	if err := res.ToolCalls[0].UnmarshalArgs(&parsedArgs); err != nil {
		t.Fatalf("failed to unmarshal args: %v", err)
	}
	if parsedArgs["location"] != "San Francisco" {
		t.Errorf("unexpected arg location: %v", parsedArgs["location"])
	}
}

func TestModel_Stream_FunctionCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		chunk := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{
								"functionCall": map[string]any{
									"id":   "call-stream-1",
									"name": "fetch_data",
									"args": map[string]any{
										"id": 42,
									},
								},
							},
						},
					},
					"finishReason": "STOP",
				},
			},
		}

		cBytes, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", cBytes)
		flusher.Flush()
	}))
	defer ts.Close()

	ctx := context.Background()
	m, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: ts.URL,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	stream, err := m.Stream(ctx, llm.GenerateRequest{
		Messages: []llm.Message{llm.UserMessage("fetch 42")},
		Tools: []llm.ToolDefinition{
			{Name: "fetch_data", Description: "Fetches data by id"},
		},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv failed: %v", err)
	}

	if len(chunk.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in chunk, got %d", len(chunk.ToolCalls))
	}
	if chunk.ToolCalls[0].Name != "fetch_data" || chunk.ToolCalls[0].ID != "call-stream-1" {
		t.Errorf("unexpected tool call: %+v", chunk.ToolCalls[0])
	}
	if chunk.FinishReason != llm.FinishReasonTool {
		t.Errorf("expected finish reason tool_calls, got %s", chunk.FinishReason)
	}
}

func init() {
	// Ensure tests do not fail on missing credentials when running in test environments
	_ = os.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "false")
}
