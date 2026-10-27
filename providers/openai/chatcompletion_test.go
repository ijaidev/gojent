package openai

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
	"github.com/ijaidev/gojent/core/llm/llmtest"
	"github.com/ijaidev/gojent/core/session"
)

// newTestChatModel starts a fake Chat Completions API and returns a model
// pointed at it.
func newTestChatModel(t *testing.T, handler http.HandlerFunc) *ChatCompletionModel {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	model, err := NewChatCompletion(context.Background(), ChatCompletionConfig{
		Model:   "gpt-test",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewChatCompletion: %v", err)
	}
	return model
}

// chatHandler serves the canned non-streaming completion unless the request
// asked for a stream, in which case it serves the canned SSE chunks.
func chatHandler(t *testing.T, response map[string]any, chunks ...map[string]any) http.HandlerFunc {
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
			writeChatSSE(t, w, chunks...)
			return
		}
		writeJSON(t, w, response)
	}
}

// writeChatSSE writes Chat Completions data-only SSE frames.
func writeChatSSE(t *testing.T, w http.ResponseWriter, chunks ...map[string]any) {
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
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		t.Errorf("write done: %v", err)
		return
	}
	flusher.Flush()
}

func chatUsageFixture() map[string]any {
	return map[string]any{
		"prompt_tokens":     10,
		"completion_tokens": 5,
		"total_tokens":      15,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 3,
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 1,
		},
	}
}

func chatCompletionFixture(message map[string]any, finishReason string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl_1",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "gpt-test",
		"choices": []any{
			map[string]any{
				"index":         0,
				"finish_reason": finishReason,
				"logprobs":      nil,
				"message":       message,
			},
		},
		"usage": chatUsageFixture(),
	}
}

func chatChunk(delta map[string]any, finishReason string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl_1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "gpt-test",
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
				"logprobs":      nil,
			},
		},
	}
}

func chatUsageChunk() map[string]any {
	return map[string]any{
		"id":      "chatcmpl_1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "gpt-test",
		"choices": []any{},
		"usage":   chatUsageFixture(),
	}
}

func chatToolCallDelta(index int, id, name, arguments string) map[string]any {
	function := map[string]any{"arguments": arguments}
	if name != "" {
		function["name"] = name
	}
	call := map[string]any{
		"index":    index,
		"function": function,
	}
	if id != "" {
		call["id"] = id
		call["type"] = "function"
	}
	return map[string]any{
		"id":      "chatcmpl_1",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "gpt-test",
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{"tool_calls": []any{call}},
				"finish_reason": "",
				"logprobs":      nil,
			},
		},
	}
}

// TestChatCompletionConformanceText proves Generate and Stream agree on a
// plain text turn, including usage and finish reason.
func TestChatCompletionConformanceText(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	response := chatCompletionFixture(map[string]any{
		"role":       "assistant",
		"content":    "checking the weather",
		"refusal":    nil,
		"tool_calls": nil,
	}, "stop")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestChatModel(t, chatHandler(t, response,
			chatChunk(map[string]any{"role": "assistant", "content": ""}, ""),
			chatChunk(map[string]any{"content": "checking the "}, ""),
			chatChunk(map[string]any{"content": "weather"}, ""),
			chatChunk(map[string]any{}, "stop"),
			chatUsageChunk(),
		))
	},
		llmtest.Case{
			Name:            "Text",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("weather?")}},
			Text:            "checking the weather",
			Usage:           &usage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "stop",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestChatCompletionConformanceToolCall proves Generate and Stream agree on a
// function call assembled from streamed argument fragments.
func TestChatCompletionConformanceToolCall(t *testing.T) {
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 1, CachedTokens: 3}
	response := chatCompletionFixture(map[string]any{
		"role":    "assistant",
		"content": "",
		"refusal": nil,
		"tool_calls": []any{
			map[string]any{
				"id":   "call_1",
				"type": "function",
				"function": map[string]any{
					"name":      "weather",
					"arguments": `{"city":"Tokyo"}`,
				},
			},
		},
	}, "tool_calls")

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return newTestChatModel(t, chatHandler(t, response,
			chatChunk(map[string]any{"role": "assistant", "content": ""}, ""),
			chatToolCallDelta(0, "call_1", "weather", ""),
			chatToolCallDelta(0, "", "", `{"city":`),
			chatToolCallDelta(0, "", "", `"Tokyo"}`),
			chatChunk(map[string]any{}, "tool_calls"),
			chatUsageChunk(),
		))
	},
		llmtest.Case{
			Name: "ToolCall",
			Request: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("weather?")},
				Tools: []llm.ToolDefinition{{
					Name:       "weather",
					Parameters: map[string]any{"type": "object"},
				}},
			},
			ToolCalls:       1,
			Usage:           &usage,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "tool_calls",
			Status:          llm.MessageStatusCompleted,
		},
	)
}

// TestChatCompletionRequestShape locks the on-the-wire request: system
// hoisting, image encoding, tools, tool choice, structured output, reasoning,
// and sampling parameters.
func TestChatCompletionRequestShape(t *testing.T) {
	var captured map[string]any
	response := chatCompletionFixture(map[string]any{
		"role":       "assistant",
		"content":    "done",
		"refusal":    nil,
		"tool_calls": nil,
	}, "stop")

	model := newTestChatModel(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "decode failed", http.StatusBadRequest)
			return
		}
		writeJSON(t, w, response)
	})

	_, err := model.Generate(context.Background(), &llm.Request{
		Messages: []llm.Message{
			llm.SystemMessage("follow the rules"),
			llm.DeveloperMessage("be terse"),
			llm.UserMessageWithParts(
				llm.NewTextPart("describe this"),
				llm.FileFromBytes([]byte("png-bytes"), "image/png"),
			),
		},
		Tools: []llm.ToolDefinition{{
			Name:        "lookup",
			Description: "find a thing",
			Strict:      true,
			Parameters:  map[string]any{"type": "object"},
		}},
		ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceRequired},
		Reasoning:  &llm.ReasoningConfig{Level: llm.ReasoningLevelMedium},
		ResponseFormat: &llm.ResponseFormat{
			Type:   llm.ResponseFormatJSONSchema,
			Name:   "answer",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
		Temperature: floatPtr(0.2),
		TopP:        floatPtr(0.9),
		MaxTokens:   intPtr(128),
		Stop:        []string{"END"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if captured["model"] != "gpt-test" {
		t.Errorf("model = %v", captured["model"])
	}
	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", captured["messages"])
	}
	system, _ := messages[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "follow the rules\n\nbe terse" {
		t.Errorf("system message = %#v", messages[0])
	}
	user, _ := messages[1].(map[string]any)
	if user["role"] != "user" {
		t.Errorf("user message = %#v", messages[1])
	}
	parts, ok := user["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("user content = %#v", user["content"])
	}
	text, _ := parts[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "describe this" {
		t.Errorf("text part = %#v", parts[0])
	}
	image, _ := parts[1].(map[string]any)
	imageURL, _ := image["image_url"].(map[string]any)
	if image["type"] != "image_url" || !strings.HasPrefix(fmt.Sprint(imageURL["url"]), "data:image/png;base64,") {
		t.Errorf("image part = %#v", parts[1])
	}

	tools, _ := captured["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", captured["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	function, _ := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "lookup" || function["strict"] != true {
		t.Errorf("tool = %#v", tools[0])
	}
	if captured["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v", captured["tool_choice"])
	}

	format, _ := captured["response_format"].(map[string]any)
	schema, _ := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["name"] != "answer" || schema["strict"] != true {
		t.Errorf("response_format = %#v", captured["response_format"])
	}
	if captured["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort = %v", captured["reasoning_effort"])
	}
	if captured["temperature"] != 0.2 || captured["top_p"] != 0.9 {
		t.Errorf("temperature/top_p = %v/%v", captured["temperature"], captured["top_p"])
	}
	if captured["max_tokens"] != float64(128) {
		t.Errorf("max_tokens = %v", captured["max_tokens"])
	}
	stop, _ := captured["stop"].([]any)
	if len(stop) != 1 || stop[0] != "END" {
		t.Errorf("stop = %#v", captured["stop"])
	}
}

// TestChatCompletionRejectsUnsupportedFeatures proves the adapter fails loudly
// instead of dropping features Chat Completions cannot represent.
func TestChatCompletionRejectsUnsupportedFeatures(t *testing.T) {
	model := newTestChatModel(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP request")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	})

	providerOptions := make(llm.ProviderData)
	if err := providerOptions.Set(chatProviderName, map[string]any{"store": true}); err != nil {
		t.Fatalf("set provider options: %v", err)
	}

	tests := []struct {
		name    string
		request *llm.Request
	}{
		{
			name: "server tool",
			request: &llm.Request{Tools: []llm.ToolDefinition{{
				Name: "web_search",
				Kind: llm.ToolKindServer,
			}}},
		},
		{
			name: "custom tool",
			request: &llm.Request{Tools: []llm.ToolDefinition{{
				Name: "shell",
				Kind: llm.ToolKindCustom,
			}}},
		},
		{
			name:    "server state",
			request: &llm.Request{State: llm.State{Mode: llm.StateModeServer}},
		},
		{
			name:    "previous response id",
			request: &llm.Request{State: llm.State{PreviousID: "resp_1"}},
		},
		{
			name:    "reasoning budget",
			request: &llm.Request{Reasoning: &llm.ReasoningConfig{BudgetTokens: intPtr(10)}},
		},
		{
			name:    "disabled reasoning",
			request: &llm.Request{Reasoning: &llm.ReasoningConfig{Level: llm.ReasoningLevelNone}},
		},
		{
			name:    "provider options",
			request: &llm.Request{ProviderOptions: providerOptions},
		},
		{
			name:    "required tool choice without tools",
			request: &llm.Request{ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceRequired}},
		},
		{
			name:    "named tool choice without tools",
			request: &llm.Request{ToolChoice: llm.ToolChoice{Name: "missing"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := model.Generate(context.Background(), test.request)
			if !errors.Is(err, llm.ErrUnsupported) {
				t.Fatalf("Generate error = %v, want ErrUnsupported", err)
			}
		})
	}
}

// TestChatCompletionMapsHTTPError checks that API failures keep their status
// and canonical kind.
func TestChatCompletionMapsHTTPError(t *testing.T) {
	model := newTestChatModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","type":"rate_limit_error"}}`))
	})

	_, err := model.Generate(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("Generate error = %T, want *llm.Error", err)
	}
	if llmErr.Kind != llm.KindRateLimit || llmErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %+v", llmErr)
	}
}

// TestChatCompletionStreamRejectsEmptyStream proves a stream that ends without
// any content surfaces a canonical error.
func TestChatCompletionStreamRejectsEmptyStream(t *testing.T) {
	model := newTestChatModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeChatSSE(t, w, chatChunk(map[string]any{"role": "assistant", "content": ""}, "stop"))
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			t.Fatal("stream ended without an error")
		}
		if err != nil {
			if !errors.Is(err, llm.ErrInvalidMessage) {
				t.Fatalf("Recv error = %v, want ErrInvalidMessage", err)
			}
			return
		}
	}
}

func TestChatCompletionSendsConfiguredHeaders(t *testing.T) {
	var got http.Header
	handler := chatHandler(t, chatCompletionFixture(map[string]any{
		"role":       "assistant",
		"content":    "hello",
		"refusal":    nil,
		"tool_calls": nil,
	}, "stop"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	model, err := NewChatCompletion(context.Background(), ChatCompletionConfig{
		Model:   "gpt-test",
		APIKey:  "test-key",
		BaseURL: server.URL,
		Headers: http.Header{
			"X-Static":   {"static"},
			"X-Tenant":   {"static-tenant"},
			"User-Agent": {"codjent/1.0"},
		},
		HeaderFunc: func(ctx context.Context) (http.Header, error) {
			sess, ok := session.FromContext(ctx)
			if !ok {
				return nil, errors.New("missing session")
			}
			return http.Header{
				"X-Tenant":  {sess.ResourceID},
				"X-Session": {sess.ID},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewChatCompletion: %v", err)
	}

	ctx := session.NewContext(context.Background(), session.Session{ID: "thread-1", ResourceID: "tenant-a"})
	if _, err := model.Generate(ctx, &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if value := got.Get("X-Static"); value != "static" {
		t.Errorf("X-Static = %q, want static", value)
	}
	if value := got.Get("X-Tenant"); value != "tenant-a" {
		t.Errorf("X-Tenant = %q, want tenant-a", value)
	}
	if value := got.Get("X-Session"); value != "thread-1" {
		t.Errorf("X-Session = %q, want thread-1", value)
	}
	if value := got.Get("User-Agent"); value != "codjent/1.0" {
		t.Errorf("User-Agent = %q, want codjent/1.0", value)
	}
}

func TestChatCompletionStreamSendsConfiguredHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeChatSSE(t, w,
			chatChunk(map[string]any{"role": "assistant", "content": "hello"}, "stop"),
			chatUsageChunk(),
		)
	}))
	t.Cleanup(server.Close)

	model, err := NewChatCompletion(context.Background(), ChatCompletionConfig{
		Model:   "gpt-test",
		APIKey:  "test-key",
		BaseURL: server.URL,
		Headers: http.Header{"X-Static": {"static"}},
		HeaderFunc: func(ctx context.Context) (http.Header, error) {
			sess, ok := session.FromContext(ctx)
			if !ok {
				return nil, errors.New("missing session")
			}
			return http.Header{"X-Session": {sess.ID}}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewChatCompletion: %v", err)
	}

	ctx := session.NewContext(context.Background(), session.Session{ID: "thread-1"})
	stream, err := model.Stream(ctx, &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Recv: %v", err)
		}
	}

	if value := got.Get("X-Static"); value != "static" {
		t.Errorf("X-Static = %q, want static", value)
	}
	if value := got.Get("X-Session"); value != "thread-1" {
		t.Errorf("X-Session = %q, want thread-1", value)
	}
}

func TestNewChatCompletionRejectsInvalidHeaders(t *testing.T) {
	_, err := NewChatCompletion(context.Background(), ChatCompletionConfig{
		Model:   "gpt-test",
		Headers: http.Header{"Bad Name": {"value"}},
	})
	if err == nil || !strings.Contains(err.Error(), "openai:") {
		t.Fatalf("NewChatCompletion error = %v, want an openai-prefixed error", err)
	}
}
