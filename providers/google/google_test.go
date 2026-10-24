package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/google"
	"google.golang.org/genai"
)

func TestNew_Validation(t *testing.T) {
	if _, err := google.New(context.Background(), google.Config{APIKey: "key"}); !errors.Is(err, google.ErrModelNotSet) {
		t.Fatalf("expected ErrModelNotSet, got %v", err)
	}

	client := &genai.Client{}
	m, err := google.New(context.Background(), google.Config{Model: "gemini-test", Client: client})
	if err != nil {
		t.Fatalf("New with client: %v", err)
	}
	if m.ModelID() != "gemini-test" {
		t.Errorf("ModelID = %q", m.ModelID())
	}
}

func TestNew_DoesNotMutateCallerClientConfig(t *testing.T) {
	cfg := &genai.ClientConfig{
		APIKey:      "client-key",
		HTTPOptions: genai.HTTPOptions{BaseURL: "https://example.invalid"},
	}
	if _, err := google.New(context.Background(), google.Config{Model: "gemini-test", ClientConfig: cfg}); err != nil {
		t.Fatalf("New: %v", err)
	}
	if cfg.APIKey != "client-key" || cfg.HTTPOptions.BaseURL != "https://example.invalid" {
		t.Errorf("caller client config was mutated: %+v", cfg)
	}
}

func TestGenerate_ContextCancellation(t *testing.T) {
	m, err := google.New(context.Background(), google.Config{Model: "gemini-test", Client: &genai.Client{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.Generate(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Generate err = %v, want context.Canceled", err)
	}
	if _, err := m.Stream(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Stream err = %v, want context.Canceled", err)
	}
}

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

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestGenerate_EndToEndAndRequestConversion(t *testing.T) {
	var captured map[string]any
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{"text": "reasoning", "thought": true},
							{"text": "Gemini response"},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":        15,
				"candidatesTokenCount":    25,
				"totalTokenCount":         40,
				"thoughtsTokenCount":      7,
				"cachedContentTokenCount": 3,
			},
		})
	})

	temperature := 0.7
	topP := 0.9
	maxTokens := 100
	resp, err := m.Generate(context.Background(), &llm.Request{
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful"),
			llm.UserMessage("Hello"),
		},
		Tools: []llm.ToolDefinition{{
			Name:        "lookup",
			Description: "Look something up",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []string{"query"},
			},
		}},
		ResponseFormat: &llm.ResponseFormat{
			Type:   llm.ResponseFormatJSONSchema,
			Name:   "Answer",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
			Strict: true,
		},
		Temperature: &temperature,
		TopP:        &topP,
		MaxTokens:   &maxTokens,
		StopWords:   []string{"END"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if resp.Message.Text() != "Gemini response" {
		t.Errorf("text = %q", resp.Message.Text())
	}
	if resp.Message.Thinking != "reasoning" {
		t.Errorf("thinking = %q", resp.Message.Thinking)
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	if resp.Usage != (llm.Usage{PromptTokens: 15, CompletionTokens: 25, TotalTokens: 40, ReasoningTokens: 7, CachedTokens: 3}) {
		t.Errorf("usage = %+v", resp.Usage)
	}

	system, ok := captured["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("systemInstruction missing: %+v", captured)
	}
	systemParts := system["parts"].([]any)
	if systemParts[0].(map[string]any)["text"] != "You are helpful" {
		t.Errorf("unexpected system instruction: %+v", system)
	}
	contents := captured["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1 user message", len(contents))
	}
	if contents[0].(map[string]any)["role"] != "user" {
		t.Errorf("unexpected user content: %+v", contents[0])
	}

	generation := captured["generationConfig"].(map[string]any)
	if generation["temperature"] != 0.7 || generation["topP"] != 0.9 {
		t.Errorf("sampling config = %+v", generation)
	}
	if generation["maxOutputTokens"] != float64(100) {
		t.Errorf("maxOutputTokens = %v", generation["maxOutputTokens"])
	}
	if generation["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v", generation["responseMimeType"])
	}
	if generation["responseJsonSchema"] == nil {
		t.Errorf("responseJsonSchema missing: %+v", generation)
	}
	stop := generation["stopSequences"].([]any)
	if len(stop) != 1 || stop[0] != "END" {
		t.Errorf("stopSequences = %+v", stop)
	}

	tools := captured["tools"].([]any)
	decl := tools[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["name"] != "lookup" || decl["description"] != "Look something up" {
		t.Errorf("unexpected function declaration: %+v", decl)
	}
	if decl["parametersJsonSchema"] == nil {
		t.Errorf("parametersJsonSchema missing: %+v", decl)
	}
}

func TestGenerate_ToolCalls(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"functionCall": map[string]any{"id": "call-a", "name": "first", "args": map[string]any{"n": 1}}},
						{"functionCall": map[string]any{"id": "call-b", "name": "second", "args": map[string]any{"s": "two"}}},
					},
				},
				"finishReason": "STOP",
			}},
		})
	})

	resp, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("go")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.FinishReason != llm.FinishReasonTool {
		t.Errorf("finish = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "call-a" || resp.Message.ToolCalls[0].Index != 0 {
		t.Errorf("first call = %+v", resp.Message.ToolCalls[0])
	}
	if resp.Message.ToolCalls[1].ID != "call-b" || resp.Message.ToolCalls[1].Index != 1 {
		t.Errorf("second call = %+v", resp.Message.ToolCalls[1])
	}
	var args map[string]any
	if err := resp.Message.ToolCalls[0].UnmarshalArgs(&args); err != nil {
		t.Fatalf("UnmarshalArgs: %v", err)
	}
	if args["n"] != float64(1) {
		t.Errorf("args = %+v", args)
	}
	if resp.Message.ProviderData == nil || resp.Message.ProviderData.Provider != "google" {
		t.Errorf("provider data missing: %+v", resp.Message.ProviderData)
	}
}

func TestGenerate_ToolResultsUseUserRole(t *testing.T) {
	var captured map[string]any
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "done"}}},
				"finishReason": "STOP",
			}},
		})
	})

	_, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessage("weather?"),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "weather", Arguments: `{"city":"Tokyo"}`},
		}},
		{Role: llm.RoleTool, Name: "weather", ToolCallID: "call-1", Content: []llm.Part{llm.TextPart(`{"temp":20}`)}},
		{Role: llm.RoleTool, Name: "weather", ToolCallID: "call-2", Content: []llm.Part{llm.TextPart(`{"temp":21}`)}},
	}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	contents := captured["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %d, want user, model, merged tool responses", len(contents))
	}
	if contents[1].(map[string]any)["role"] != "model" {
		t.Errorf("assistant role = %v", contents[1].(map[string]any)["role"])
	}
	toolTurn := contents[2].(map[string]any)
	if toolTurn["role"] != "user" {
		t.Errorf("tool turn role = %v, want user", toolTurn["role"])
	}
	parts := toolTurn["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("tool response parts = %d, want 2 merged responses", len(parts))
	}
	for i, raw := range parts {
		response := raw.(map[string]any)["functionResponse"].(map[string]any)
		if response["name"] != "weather" {
			t.Errorf("response %d name = %v", i, response["name"])
		}
	}
}

func TestGenerate_MultipartValidation(t *testing.T) {
	m, err := google.New(context.Background(), google.Config{Model: "gemini-test", Client: &genai.Client{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessageWithParts(llm.TextPart("look"), llm.Part{Type: llm.PartTypeImage, Data: []byte{1}, MIMEType: "image/nope"}),
	}})
	if !errors.Is(err, llm.ErrUnsupportedMIMEType) {
		t.Errorf("unsupported MIME err = %v", err)
	}

	_, err = m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessageWithParts(llm.Part{Type: llm.PartTypeImage, URL: "https://example.com/x.png"}),
	}})
	if !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Errorf("direct URL err = %v", err)
	}

	_, err = m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessageWithParts(llm.Part{Type: llm.PartTypeImage}),
	}})
	if !errors.Is(err, llm.ErrInvalidPart) {
		t.Errorf("empty image err = %v", err)
	}
}

func TestGenerate_BlockedPrompt(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"promptFeedback": map[string]any{
				"blockReason":        "SAFETY",
				"blockReasonMessage": "blocked",
			},
		})
	})

	_, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("bad")}})
	if !errors.Is(err, google.ErrResponseBlocked) {
		t.Fatalf("err = %v, want ErrResponseBlocked", err)
	}
}

func TestStream_DeltasThinkingToolsAndProviderData(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		chunks := []map[string]any{
			{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "Chunk 1 "}}},
			}}},
			{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "thinking", "thought": true}}},
			}}},
			{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{
					{"functionCall": map[string]any{"id": "call-s", "name": "fetch", "args": map[string]any{"id": 42}}},
				}},
				"finishReason": "STOP",
			}}, "usageMetadata": map[string]any{"promptTokenCount": 4, "candidatesTokenCount": 6, "totalTokenCount": 10}},
		}
		for _, chunk := range chunks {
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		}
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("go")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var text, thinking string
	var calls []llm.ToolCall
	var usage llm.Usage
	var finish llm.FinishReason
	var providerData *llm.ProviderData
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		text += chunk.Delta
		thinking += chunk.ThinkingDelta
		calls = append(calls, chunk.ToolCalls...)
		if chunk.Usage != nil {
			usage = usage.Add(*chunk.Usage)
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
		if chunk.ProviderData != nil {
			providerData = chunk.ProviderData
		}
	}
	if text != "Chunk 1 " {
		t.Errorf("text = %q", text)
	}
	if thinking != "thinking" {
		t.Errorf("thinking = %q", thinking)
	}
	if len(calls) != 1 || calls[0].ID != "call-s" || calls[0].Name != "fetch" {
		t.Errorf("tool calls = %+v", calls)
	}
	if usage.TotalTokens != 10 {
		t.Errorf("usage = %+v", usage)
	}
	if finish != llm.FinishReasonTool {
		t.Errorf("finish = %q", finish)
	}
	if providerData == nil || providerData.Provider != "google" {
		t.Errorf("provider data = %+v", providerData)
	}
	if !strings.Contains(string(providerData.Data), "call-s") {
		t.Errorf("provider data does not preserve streamed parts: %s", providerData.Data)
	}
}

func TestStream_BlockedPrompt(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunk := map[string]any{
			"promptFeedback": map[string]any{"blockReason": "SAFETY"},
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("bad")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	_, err = stream.Recv()
	if !errors.Is(err, google.ErrResponseBlocked) {
		t.Fatalf("Recv err = %v, want ErrResponseBlocked", err)
	}
}

func TestThinkingConfiguration(t *testing.T) {
	t.Run("level", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		include := true
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages: []llm.Message{llm.UserMessage("go")},
			Thinking: &llm.ThinkingConfig{Level: llm.ThinkingLevelHigh, IncludeThoughts: &include},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != "HIGH" {
			t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
		}
		if thinking["includeThoughts"] != true {
			t.Errorf("includeThoughts = %v", thinking["includeThoughts"])
		}
		if thinking["thinkingBudget"] != nil {
			t.Errorf("thinkingBudget should not be set with a level: %+v", thinking)
		}
	})

	t.Run("budget", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		budget := 128
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages: []llm.Message{llm.UserMessage("go")},
			Thinking: &llm.ThinkingConfig{BudgetTokens: &budget},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingBudget"] != float64(128) {
			t.Errorf("thinkingBudget = %v", thinking["thinkingBudget"])
		}
	})
}

func TestResponseFormats(t *testing.T) {
	tests := []struct {
		name     string
		format   *llm.ResponseFormat
		wantMIME string
	}{
		{name: "json", format: &llm.ResponseFormat{Type: llm.ResponseFormatJSON}, wantMIME: "application/json"},
		{name: "schema", format: &llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: map[string]any{"type": "object"}}, wantMIME: "application/json"},
		{name: "text", format: &llm.ResponseFormat{Type: llm.ResponseFormatText}, wantMIME: "text/plain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &captured)
				writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
					"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
				}}})
			})
			_, err := m.Generate(context.Background(), &llm.Request{
				Messages:       []llm.Message{llm.UserMessage("go")},
				ResponseFormat: tc.format,
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got := captured["generationConfig"].(map[string]any)["responseMimeType"]; got != tc.wantMIME {
				t.Errorf("responseMimeType = %v, want %v", got, tc.wantMIME)
			}
		})
	}
}

func TestThinkingLevels(t *testing.T) {
	levels := google.AvailableThinkingLevels()
	if len(levels) == 0 {
		t.Fatal("expected thinking levels")
	}
	if levels[0] != google.ThinkingLevelMinimal {
		t.Errorf("first level = %v", levels[0])
	}
}
