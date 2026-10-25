package google_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

func TestInfo(t *testing.T) {
	m, err := google.New(context.Background(), google.Config{Model: "gemini-test", Client: &genai.Client{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info := m.Info()
	if info.Provider != "google" || info.Model != "gemini-test" {
		t.Errorf("info = %+v", info)
	}
	features := info.Features
	if !features.Streaming || !features.Tools || !features.ParallelToolCalls || !features.StrictJSONSchema {
		t.Errorf("core features not advertised: %+v", features)
	}
	if !features.Reasoning || !features.ReasoningSummary {
		t.Errorf("reasoning features not advertised: %+v", features)
	}
	if !features.Vision || !features.AudioIn || !features.Video || !features.Documents {
		t.Errorf("media features not advertised: %+v", features)
	}
	if features.PromptCaching != llm.CacheModeAutomatic {
		t.Errorf("prompt caching = %q", features.PromptCaching)
	}
	if features.ServerState || features.HostedTools || features.MCP || features.AudioOut || features.ReasoningEncrypted {
		t.Errorf("unsupported features advertised: %+v", features)
	}
}

func TestGenerate_EndToEndAndRequestConversion(t *testing.T) {
	var captured map[string]any
	signature := []byte("sig")
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		captured = readBody(t, r)
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"text": "reasoning", "thought": true},
						{"text": "Gemini response", "thoughtSignature": base64.StdEncoding.EncodeToString(signature)},
					},
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": usageBody(),
			"modelVersion":  "gemini-test-001",
		})
	})

	temperature := 0.7
	topP := 0.9
	maxTokens := 100
	resp, err := m.Generate(context.Background(), &llm.Request{
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful"),
			llm.DeveloperMessage("be terse"),
			llm.UserMessageWithParts(
				llm.NewTextPart("look"),
				llm.FileFromBytes([]byte{1, 2, 3}, "image/png"),
			),
			llm.AssistantMessage("earlier"),
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
			Schema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
			Strict: true,
		},
		Temperature: &temperature,
		TopP:        &topP,
		MaxTokens:   &maxTokens,
		Stop:        []string{"END"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got := resp.Message.Text(); got != "Gemini response" {
		t.Errorf("text = %q", got)
	}
	if len(resp.Message.Content) != 2 {
		t.Fatalf("content = %d parts, want reasoning and text", len(resp.Message.Content))
	}
	reasoning, ok := resp.Message.Content[0].(*llm.ReasoningPart)
	if !ok || reasoning.Text != "reasoning" {
		t.Errorf("reasoning = %+v", resp.Message.Content[0])
	}
	text, ok := resp.Message.Content[1].(*llm.TextPart)
	if !ok || text.Text != "Gemini response" {
		t.Errorf("text part = %+v", resp.Message.Content[1])
	}
	if !bytes.Equal(providerSignature(t, text), signature) {
		t.Errorf("thought signature = %q, want %q", providerSignature(t, text), signature)
	}
	if resp.FinishReason != llm.FinishReasonStop || resp.RawFinishReason != "STOP" {
		t.Errorf("finish = %q raw = %q", resp.FinishReason, resp.RawFinishReason)
	}
	if resp.Status != llm.MessageStatusCompleted {
		t.Errorf("status = %q", resp.Status)
	}
	if resp.Model != "gemini-test-001" {
		t.Errorf("model = %q", resp.Model)
	}
	wantUsage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}
	if resp.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", resp.Usage, wantUsage)
	}

	system, ok := captured["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("systemInstruction missing: %+v", captured)
	}
	systemParts := system["parts"].([]any)
	if len(systemParts) != 2 ||
		systemParts[0].(map[string]any)["text"] != "You are helpful" ||
		systemParts[1].(map[string]any)["text"] != "be terse" {
		t.Errorf("system instruction = %+v", systemParts)
	}

	contents := captured["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %d, want user, model, user", len(contents))
	}
	if role := contents[0].(map[string]any)["role"]; role != "user" {
		t.Errorf("first role = %v", role)
	}
	firstParts := contents[0].(map[string]any)["parts"].([]any)
	if len(firstParts) != 2 {
		t.Fatalf("first user parts = %d", len(firstParts))
	}
	inline := firstParts[1].(map[string]any)["inlineData"].(map[string]any)
	if inline["mimeType"] != "image/png" || inline["data"] != "AQID" {
		t.Errorf("inline data = %+v", inline)
	}
	if role := contents[1].(map[string]any)["role"]; role != "model" {
		t.Errorf("assistant role = %v, want model", role)
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
	if captured["toolConfig"] != nil {
		t.Errorf("unexpected toolConfig: %+v", captured["toolConfig"])
	}
}

func TestGenerate_ToolCalls(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{
							"functionCall":     map[string]any{"id": "call-a", "name": "first", "args": map[string]any{"n": 1}},
							"thoughtSignature": base64.StdEncoding.EncodeToString([]byte("sig")),
						},
						{"functionCall": map[string]any{"name": "second", "args": map[string]any{"s": "two"}}},
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
	calls := resp.Message.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(calls))
	}
	if calls[0].CallID != "call-a" || calls[0].ItemID != "" || calls[0].Kind != llm.ToolKindFunction {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[1].CallID != "call_1" {
		t.Errorf("generated call id = %q, want call_1", calls[1].CallID)
	}
	var args map[string]any
	if err := calls[0].UnmarshalInput(&args); err != nil {
		t.Fatalf("UnmarshalInput: %v", err)
	}
	if args["n"] != float64(1) {
		t.Errorf("args = %+v", args)
	}
	if got := string(providerSignature(t, resp.Message.Content[0])); got != "sig" {
		t.Errorf("call signature = %q", got)
	}
}

func TestGenerate_ToolResultsUseUserRole(t *testing.T) {
	var captured map[string]any
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		captured = readBody(t, r)
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "done"}}},
				"finishReason": "STOP",
			}},
		})
	})

	assistant := llm.AssistantMessageWithParts(
		llm.NewTextPart("calling"),
		llm.NewToolCallPart(llm.ToolCall{
			CallID: "call-1",
			Name:   "weather",
			Input:  json.RawMessage(`{"city":"Tokyo"}`),
		}),
		llm.NewToolCallPart(llm.ToolCall{
			CallID: "call-2",
			Name:   "clock",
			Input:  json.RawMessage(`{}`),
		}),
	)
	results := llm.ToolResultsMessage(
		llm.ToolResult{
			CallID:  "call-1",
			Status:  llm.ToolStatusCompleted,
			Content: []llm.Part{llm.NewTextPart(`{"temp":20}`)},
		},
		llm.ToolResult{
			CallID:     "call-2",
			Status:     llm.ToolStatusError,
			Content:    []llm.Part{llm.NewTextPart("boom")},
			Structured: json.RawMessage(`{"code":"E"}`),
		},
	)

	_, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessage("weather?"),
		assistant,
		results,
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

	first := parts[0].(map[string]any)["functionResponse"].(map[string]any)
	if first["name"] != "weather" || first["id"] != "call-1" {
		t.Errorf("first response = %+v", first)
	}
	firstBody := first["response"].(map[string]any)
	if firstBody["output"] != `{"temp":20}` {
		t.Errorf("first output = %+v", firstBody)
	}

	second := parts[1].(map[string]any)["functionResponse"].(map[string]any)
	if second["name"] != "clock" || second["id"] != "call-2" {
		t.Errorf("second response = %+v", second)
	}
	secondBody := second["response"].(map[string]any)
	if secondBody["output"] != "boom" || secondBody["error"] != "boom" || secondBody["code"] != "E" {
		t.Errorf("second body = %+v", secondBody)
	}
}

func TestGenerate_ToolResultFile(t *testing.T) {
	var captured map[string]any
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		captured = readBody(t, r)
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
				"finishReason": "STOP",
			}},
		})
	})

	assistant := llm.AssistantMessageWithParts(
		llm.NewToolCallPart(llm.ToolCall{CallID: "call-1", Name: "snapshot", Input: json.RawMessage(`{}`)}),
	)
	results := llm.ToolResultsMessage(llm.ToolResult{
		CallID:  "call-1",
		Content: []llm.Part{llm.NewTextPart("here"), llm.FileFromBytes([]byte{1, 2}, "image/png")},
	})
	if _, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
		llm.UserMessage("snap"),
		assistant,
		results,
	}}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	contents := captured["contents"].([]any)
	response := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	parts, ok := response["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("function response parts = %+v", response)
	}
	inline := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if inline["mimeType"] != "image/png" || inline["data"] != "AQI=" {
		t.Errorf("inline function response = %+v", inline)
	}
}

func TestGenerate_FileParts(t *testing.T) {
	tests := []struct {
		name    string
		part    *llm.FilePart
		wantErr error
		check   func(*testing.T, map[string]any)
	}{
		{
			name: "inline image",
			part: llm.FileFromBytes([]byte{1, 2, 3}, "image/png"),
			check: func(t *testing.T, raw map[string]any) {
				inline := raw["inlineData"].(map[string]any)
				if inline["mimeType"] != "image/png" || inline["data"] != "AQID" {
					t.Errorf("inline = %+v", inline)
				}
			},
		},
		{
			name: "inline audio",
			part: llm.FileFromBytes([]byte{1}, "audio/mp3"),
			check: func(t *testing.T, raw map[string]any) {
				if raw["inlineData"].(map[string]any)["mimeType"] != "audio/mp3" {
					t.Errorf("inline = %+v", raw)
				}
			},
		},
		{
			name: "inline video",
			part: llm.FileFromBytes([]byte{1}, "video/mp4"),
			check: func(t *testing.T, raw map[string]any) {
				if raw["inlineData"].(map[string]any)["mimeType"] != "video/mp4" {
					t.Errorf("inline = %+v", raw)
				}
			},
		},
		{
			name: "inline document",
			part: llm.FileFromBytes([]byte{1}, "application/pdf"),
			check: func(t *testing.T, raw map[string]any) {
				if raw["inlineData"].(map[string]any)["mimeType"] != "application/pdf" {
					t.Errorf("inline = %+v", raw)
				}
			},
		},
		{
			name: "cloud storage URI",
			part: llm.FileFromURL("gs://bucket/doc.pdf", "application/pdf"),
			check: func(t *testing.T, raw map[string]any) {
				file := raw["fileData"].(map[string]any)
				if file["fileUri"] != "gs://bucket/doc.pdf" || file["mimeType"] != "application/pdf" {
					t.Errorf("file data = %+v", file)
				}
			},
		},
		{
			name: "files API URI",
			part: llm.FileFromURL("https://generativelanguage.googleapis.com/v1beta/files/abc", "text/plain"),
			check: func(t *testing.T, raw map[string]any) {
				file := raw["fileData"].(map[string]any)
				if file["fileUri"] != "https://generativelanguage.googleapis.com/v1beta/files/abc" {
					t.Errorf("file data = %+v", file)
				}
			},
		},
		{
			name:    "direct https URI",
			part:    llm.FileFromURL("https://example.com/x.png", "image/png"),
			wantErr: llm.ErrUnsupportedPart,
		},
		{
			name:    "provider file id",
			part:    llm.FileFromID("files/abc", "image/png"),
			wantErr: llm.ErrUnsupportedPart,
		},
		{
			name:    "unsupported image MIME",
			part:    llm.FileFromBytes([]byte{1}, "image/gif"),
			wantErr: llm.ErrUnsupportedMIMEType,
		},
		{
			name:    "unsupported document MIME",
			part:    llm.FileFromBytes([]byte{1}, "application/zip"),
			wantErr: llm.ErrUnsupportedMIMEType,
		},
		{
			name:    "missing bytes",
			part:    llm.FileFromBytes(nil, "image/png"),
			wantErr: llm.ErrInvalidPart,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
				captured = readBody(t, r)
				writeJSON(t, w, map[string]any{
					"candidates": []map[string]any{{
						"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
						"finishReason": "STOP",
					}},
				})
			})
			_, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{
				llm.UserMessageWithParts(tc.part),
			}})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			part := captured["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
			tc.check(t, part)
		})
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

func TestGenerate_RejectsUnsupportedFeatures(t *testing.T) {
	store := true
	negativeBudget := -1
	zeroTokens := 0

	assistantCustomCall := llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{
		CallID: "call-1",
		Name:   "shell",
		Kind:   llm.ToolKindCustom,
		Input:  json.RawMessage(`"ls"`),
	}))

	tests := []struct {
		name string
		req  *llm.Request
	}{
		{
			name: "server state",
			req: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("hi")},
				State:    llm.State{Mode: llm.StateModeServer},
			},
		},
		{
			name: "previous response id",
			req: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("hi")},
				State:    llm.State{PreviousID: "resp-1"},
			},
		},
		{
			name: "store",
			req: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("hi")},
				State:    llm.State{Store: &store},
			},
		},
		{
			name: "server tool definition",
			req: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("hi")},
				Tools:    []llm.ToolDefinition{{Name: "search", Kind: llm.ToolKindServer}},
			},
		},
		{
			name: "required tool choice without tools",
			req: &llm.Request{
				Messages:   []llm.Message{llm.UserMessage("hi")},
				ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceRequired},
			},
		},
		{
			name: "named tool choice without tools",
			req: &llm.Request{
				Messages:   []llm.Message{llm.UserMessage("hi")},
				ToolChoice: llm.ToolChoice{Name: "lookup"},
			},
		},
		{
			name: "custom tool call",
			req: &llm.Request{
				Messages: []llm.Message{llm.UserMessage("hi"), assistantCustomCall},
			},
		},
		{
			name: "unknown reasoning level",
			req: &llm.Request{
				Messages:  []llm.Message{llm.UserMessage("hi")},
				Reasoning: &llm.ReasoningConfig{Level: "ultra"},
			},
		},
		{
			name: "negative reasoning budget",
			req: &llm.Request{
				Messages:  []llm.Message{llm.UserMessage("hi")},
				Reasoning: &llm.ReasoningConfig{BudgetTokens: &negativeBudget},
			},
		},
		{
			name: "zero max tokens",
			req: &llm.Request{
				Messages:  []llm.Message{llm.UserMessage("hi")},
				MaxTokens: &zeroTokens,
			},
		},
		{
			name: "schema format without schema",
			req: &llm.Request{
				Messages:       []llm.Message{llm.UserMessage("hi")},
				ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema},
			},
		},
		{
			name: "non-text system part",
			req: &llm.Request{
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: []llm.Part{llm.FileFromBytes([]byte{1}, "image/png")}},
					llm.UserMessage("hi"),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t, func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("unexpected HTTP request")
			})
			if _, err := m.Generate(context.Background(), tc.req); !errors.Is(err, llm.ErrUnsupported) &&
				!errors.Is(err, llm.ErrUnsupportedPartType) {
				t.Fatalf("err = %v, want ErrUnsupported or ErrUnsupportedPartType", err)
			}
		})
	}
}

func TestGenerate_AllowsNoopToolChoiceWithoutTools(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
				"finishReason": "STOP",
			}},
		})
	})
	for _, choice := range []llm.ToolChoiceMode{llm.ToolChoiceNone, llm.ToolChoiceAuto} {
		if _, err := m.Generate(context.Background(), &llm.Request{
			Messages:   []llm.Message{llm.UserMessage("hi")},
			ToolChoice: llm.ToolChoice{Mode: choice},
		}); err != nil {
			t.Errorf("tool choice %q: %v", choice, err)
		}
	}
}

func TestGenerate_FirstCandidateOnly(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{
				{
					"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "first"}}},
					"finishReason": "STOP",
				},
				{
					"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "second"}}},
					"finishReason": "STOP",
				},
			},
		})
	})
	resp, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := resp.Message.Text(); got != "first" {
		t.Errorf("text = %q, want first", got)
	}
}

func TestGenerate_FinishReasons(t *testing.T) {
	tests := []struct {
		raw    string
		want   llm.FinishReason
		status llm.MessageStatus
	}{
		{raw: "STOP", want: llm.FinishReasonStop, status: llm.MessageStatusCompleted},
		{raw: "MAX_TOKENS", want: llm.FinishReasonLength, status: llm.MessageStatusIncomplete},
		{raw: "SAFETY", want: llm.FinishReasonContentFilter, status: llm.MessageStatusIncomplete},
		{raw: "RECITATION", want: llm.FinishReasonContentFilter, status: llm.MessageStatusIncomplete},
		{raw: "MALFORMED_FUNCTION_CALL", want: llm.FinishReason("malformed_function_call"), status: llm.MessageStatusIncomplete},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, map[string]any{
					"candidates": []map[string]any{{
						"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "partial"}}},
						"finishReason": tc.raw,
					}},
				})
			})
			resp, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if resp.FinishReason != tc.want {
				t.Errorf("finish = %q, want %q", resp.FinishReason, tc.want)
			}
			if resp.RawFinishReason != tc.raw {
				t.Errorf("raw finish = %q, want %q", resp.RawFinishReason, tc.raw)
			}
			if resp.Status != tc.status {
				t.Errorf("status = %q, want %q", resp.Status, tc.status)
			}
		})
	}
}

func TestGenerate_NoContent(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"role": "model", "parts": []map[string]any{}}}},
		})
	})
	if _, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}}); !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("err = %v, want ErrInvalidMessage", err)
	}
}

func TestStream_IncrementalText(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{"text": "Hel"}}, "", nil),
			streamCandidate([]map[string]any{{"text": "lo"}}, "", nil),
			streamCandidate([]map[string]any{{"text": "!"}}, "STOP", usageBody()),
		)
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collectStream(t, stream)

	if _, ok := events[0].(llm.MessageStart); !ok {
		t.Fatalf("first event = %T, want MessageStart", events[0])
	}
	var replaces int
	for _, event := range events {
		if delta, ok := event.(llm.PartDelta); ok && delta.Replace {
			replaces++
		}
	}
	if replaces != 0 {
		t.Errorf("Replace deltas = %d, want 0 for incremental text", replaces)
	}

	assembled := assembleEvents(t, events)
	if got := assembled.Message.Text(); got != "Hello!" {
		t.Errorf("text = %q", got)
	}
	if assembled.Usage != (llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, ReasoningTokens: 2, CachedTokens: 1}) {
		t.Errorf("usage = %+v", assembled.Usage)
	}
	if assembled.FinishReason != llm.FinishReasonStop || assembled.RawFinishReason != "STOP" {
		t.Errorf("finish = %q raw = %q", assembled.FinishReason, assembled.RawFinishReason)
	}
}

func TestStream_CumulativeText(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{"text": "Hel"}}, "", nil),
			streamCandidate([]map[string]any{{"text": "Hello"}}, "", nil),
			streamCandidate([]map[string]any{{"text": "Hello!"}}, "STOP", nil),
		)
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collectStream(t, stream)

	var snapshots []string
	for _, event := range events {
		if delta, ok := event.(llm.PartDelta); ok && delta.Replace {
			snapshots = append(snapshots, delta.Text)
		}
	}
	if len(snapshots) != 2 || snapshots[0] != "Hello" || snapshots[1] != "Hello!" {
		t.Errorf("snapshots = %+v", snapshots)
	}

	assembled := assembleEvents(t, events)
	if got := assembled.Message.Text(); got != "Hello!" {
		t.Errorf("text = %q", got)
	}
}

func TestStream_ReasoningThenText(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{"text": "think", "thought": true}}, "", nil),
			streamCandidate([]map[string]any{{"text": "answer"}}, "", nil),
			streamCandidate([]map[string]any{{"text": " done"}}, "STOP", nil),
		)
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := assembleEvents(t, collectStream(t, stream))
	if got := reasoningText(assembled.Message); got != "think" {
		t.Errorf("reasoning = %q", got)
	}
	if got := assembled.Message.Text(); got != "answer done" {
		t.Errorf("text = %q", got)
	}
}

func TestStream_CumulativeAddsToolCall(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{"text": "I will check"}}, "", nil),
			streamCandidate([]map[string]any{
				{"text": "I will check the weather"},
				{"functionCall": map[string]any{"id": "call-1", "name": "weather", "args": map[string]any{"city": "Tokyo"}}},
			}, "STOP", usageBody()),
		)
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := assembleEvents(t, collectStream(t, stream))
	if got := assembled.Message.Text(); got != "I will check the weather" {
		t.Errorf("text = %q", got)
	}
	calls := assembled.Message.ToolCalls()
	if len(calls) != 1 || calls[0].CallID != "call-1" || calls[0].Name != "weather" {
		t.Fatalf("calls = %+v", calls)
	}
	if assembled.FinishReason != llm.FinishReasonTool {
		t.Errorf("finish = %q", assembled.FinishReason)
	}
}

func TestStream_TwoToolCalls(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{
				{"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{"q": "go"}}},
			}, "", nil),
			streamCandidate([]map[string]any{
				{"functionCall": map[string]any{"id": "call-2", "name": "clock", "args": map[string]any{}}},
			}, "STOP", nil),
		)
	})

	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collectStream(t, stream)
	starts := 0
	for _, event := range events {
		if _, ok := event.(llm.MessageStart); ok {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("MessageStart events = %d, want 1", starts)
	}
	assembled := assembleEvents(t, events)
	calls := assembled.Message.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].CallID != "call-1" || calls[1].CallID != "call-2" {
		t.Errorf("call ids = %q, %q", calls[0].CallID, calls[1].CallID)
	}
}

func TestStream_ThoughtSignature(t *testing.T) {
	signature := base64.StdEncoding.EncodeToString([]byte("sig"))
	tests := []struct {
		name    string
		chunk   map[string]any
		version string
	}{
		{
			name:  "signature on cumulative text",
			chunk: streamCandidate([]map[string]any{{"text": "answer", "thoughtSignature": signature}}, "STOP", nil),
		},
		{
			name:  "signature-only part",
			chunk: streamCandidate([]map[string]any{{"thoughtSignature": signature}}, "STOP", nil),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
				writeSSE(t, w,
					streamCandidate([]map[string]any{{"text": "think", "thought": true}}, "", nil),
					streamCandidate([]map[string]any{{"text": "answer"}}, "", nil),
					tc.chunk,
				)
			})
			stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			assembled := assembleEvents(t, collectStream(t, stream))
			if got := assembled.Message.Text(); got != "answer" {
				t.Errorf("text = %q", got)
			}
			var text *llm.TextPart
			for _, part := range assembled.Message.Content {
				if candidate, ok := part.(*llm.TextPart); ok {
					text = candidate
				}
			}
			if text == nil {
				t.Fatal("no text part")
			}
			if got := string(providerSignature(t, text)); got != "sig" {
				t.Errorf("signature = %q, want %q", got, "sig")
			}
		})
	}
}

func TestStream_UsageIsSnapshot(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{"text": "a"}}, "", map[string]any{
				"promptTokenCount": 4, "candidatesTokenCount": 6, "totalTokenCount": 10,
			}),
			streamCandidate([]map[string]any{{"text": "b"}}, "STOP", map[string]any{
				"promptTokenCount": 4, "candidatesTokenCount": 8, "totalTokenCount": 12,
			}),
		)
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := assembleEvents(t, collectStream(t, stream))
	if assembled.Usage.TotalTokens != 12 {
		t.Errorf("usage = %+v, want total 12 (latest snapshot)", assembled.Usage)
	}
}

func TestStream_BlockedPrompt(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w, map[string]any{
			"promptFeedback": map[string]any{"blockReason": "SAFETY"},
		})
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("bad")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Recv(); !errors.Is(err, google.ErrResponseBlocked) {
		t.Fatalf("Recv err = %v, want ErrResponseBlocked", err)
	}
}

func TestStream_NoContent(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w, streamCandidate(nil, "STOP", nil))
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, recvErr := stream.Recv()
		if recvErr == nil {
			continue
		}
		if !errors.Is(recvErr, llm.ErrInvalidMessage) {
			t.Fatalf("Recv err = %v, want ErrInvalidMessage", recvErr)
		}
		break
	}
}

func TestStream_RejectsPartialArguments(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w, streamCandidate([]map[string]any{{
			"functionCall": map[string]any{
				"id":          "call-1",
				"name":        "lookup",
				"partialArgs": []map[string]any{{"jsonPath": "$.q", "stringValue": "go"}},
			},
		}}, "STOP", nil))
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, recvErr := stream.Recv()
		if recvErr == nil {
			continue
		}
		if !errors.Is(recvErr, llm.ErrUnsupported) {
			t.Fatalf("Recv err = %v, want ErrUnsupported", recvErr)
		}
		break
	}
}

func TestReasoningConfiguration(t *testing.T) {
	t.Run("level and include thoughts", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		include := false
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages:  []llm.Message{llm.UserMessage("go")},
			Reasoning: &llm.ReasoningConfig{Level: llm.ReasoningLevelHigh, IncludeThoughts: &include},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != "HIGH" {
			t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
		}
		if thinking["includeThoughts"] == true {
			t.Errorf("includeThoughts = %v, want false or omitted", thinking["includeThoughts"])
		}
		if thinking["thinkingBudget"] != nil {
			t.Errorf("thinkingBudget should not be set with a level: %+v", thinking)
		}
	})

	t.Run("provider default", func(t *testing.T) {
		var captured map[string]any
		m := newModelWithConfig(t, google.Config{
			ReasoningLevel: google.ThinkingLevelLow,
		}, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		if _, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("go")}}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != "LOW" {
			t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
		}
		if thinking["includeThoughts"] != true {
			t.Errorf("includeThoughts = %v", thinking["includeThoughts"])
		}
	})

	t.Run("request overrides provider default", func(t *testing.T) {
		var captured map[string]any
		m := newModelWithConfig(t, google.Config{
			ReasoningLevel: google.ThinkingLevelLow,
		}, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages:  []llm.Message{llm.UserMessage("go")},
			Reasoning: &llm.ReasoningConfig{Level: llm.ReasoningLevelHigh},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != "HIGH" {
			t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
		}
	})

	t.Run("budget", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		budget := 128
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages:  []llm.Message{llm.UserMessage("go")},
			Reasoning: &llm.ReasoningConfig{BudgetTokens: &budget},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingBudget"] != float64(128) {
			t.Errorf("thinkingBudget = %v", thinking["thinkingBudget"])
		}
	})

	t.Run("level none disables thinking", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages:  []llm.Message{llm.UserMessage("go")},
			Reasoning: &llm.ReasoningConfig{Level: llm.ReasoningLevelNone},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingBudget"] != float64(0) {
			t.Errorf("thinkingBudget = %v, want 0", thinking["thinkingBudget"])
		}
		if thinking["thinkingLevel"] != nil {
			t.Errorf("thinkingLevel = %v, want unset", thinking["thinkingLevel"])
		}
	})

	t.Run("level wins over budget", func(t *testing.T) {
		var captured map[string]any
		m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
			captured = readBody(t, r)
			writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
			}}})
		})
		budget := 128
		_, err := m.Generate(context.Background(), &llm.Request{
			Messages: []llm.Message{llm.UserMessage("go")},
			Reasoning: &llm.ReasoningConfig{
				Level:        llm.ReasoningLevelLow,
				BudgetTokens: &budget,
			},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		thinking := captured["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		if thinking["thinkingBudget"] != nil {
			t.Errorf("thinkingBudget should be unset: %+v", thinking)
		}
		if thinking["thinkingLevel"] != "LOW" {
			t.Errorf("thinkingLevel = %v", thinking["thinkingLevel"])
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
				captured = readBody(t, r)
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

func TestToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice llm.ToolChoice
		want   map[string]any
	}{
		{
			name:   "auto",
			choice: llm.ToolChoice{Mode: llm.ToolChoiceAuto},
			want:   map[string]any{"mode": "AUTO"},
		},
		{
			name:   "none",
			choice: llm.ToolChoice{Mode: llm.ToolChoiceNone},
			want:   map[string]any{"mode": "NONE"},
		},
		{
			name:   "required",
			choice: llm.ToolChoice{Mode: llm.ToolChoiceRequired},
			want:   map[string]any{"mode": "ANY"},
		},
		{
			name:   "named",
			choice: llm.ToolChoice{Name: "lookup"},
			want:   map[string]any{"mode": "ANY", "allowedFunctionNames": []any{"lookup"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
				captured = readBody(t, r)
				writeJSON(t, w, map[string]any{"candidates": []map[string]any{{
					"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "ok"}}},
				}}})
			})
			_, err := m.Generate(context.Background(), &llm.Request{
				Messages: []llm.Message{llm.UserMessage("go")},
				Tools: []llm.ToolDefinition{{
					Name:       "lookup",
					Parameters: map[string]any{"type": "object"},
				}},
				ToolChoice: tc.choice,
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			config := captured["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
			for key, want := range tc.want {
				got, ok := config[key]
				if !ok {
					t.Errorf("%s missing from %+v", key, config)
					continue
				}
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				if !bytes.Equal(gotJSON, wantJSON) {
					t.Errorf("%s = %s, want %s", key, gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestAvailableThinkingLevels(t *testing.T) {
	levels := google.AvailableThinkingLevels()
	if len(levels) == 0 {
		t.Fatal("expected thinking levels")
	}
	if levels[0] != google.ThinkingLevelMinimal {
		t.Errorf("first level = %v", levels[0])
	}
}

func assembleEvents(t *testing.T, events []llm.StreamEvent) *llm.Response {
	t.Helper()
	assembler := llm.NewAssembler(llm.RoleAssistant)
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply(%T): %v", event, err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return response
}

func reasoningText(message llm.Message) string {
	var text string
	for _, part := range message.Content {
		if reasoning, ok := part.(*llm.ReasoningPart); ok {
			text += reasoning.Text
		}
	}
	return text
}

func TestGenerate_SignatureOnlyPart(t *testing.T) {
	signature := base64.StdEncoding.EncodeToString([]byte("sig"))
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{
					{"text": "answer"},
					{"thoughtSignature": signature},
				}},
				"finishReason": "STOP",
			}},
		})
	})
	resp, err := m.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("content = %d parts, want the signature folded into the text part", len(resp.Message.Content))
	}
	text, ok := resp.Message.Content[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("part = %T", resp.Message.Content[0])
	}
	if got := string(providerSignature(t, text)); got != "sig" {
		t.Errorf("signature = %q", got)
	}
}

func TestStream_FilePart(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{
				"inlineData": map[string]any{"mimeType": "image/png", "data": "AQI="},
			}}, "", nil),
			streamCandidate([]map[string]any{{
				"inlineData": map[string]any{"mimeType": "image/png", "data": "AQI="},
			}}, "STOP", nil),
		)
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := assembleEvents(t, collectStream(t, stream))
	if len(assembled.Message.Content) != 1 {
		t.Fatalf("content = %d parts, want one file", len(assembled.Message.Content))
	}
	file, ok := assembled.Message.Content[0].(*llm.FilePart)
	if !ok || file.MIMEType != "image/png" || !bytes.Equal(file.Data, []byte{1, 2}) {
		t.Errorf("file = %+v", assembled.Message.Content[0])
	}
}

func TestStream_IDLessToolCalls(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{
				"functionCall": map[string]any{"name": "lookup", "args": map[string]any{"a": 1}},
			}}, "", nil),
			streamCandidate([]map[string]any{{
				"functionCall": map[string]any{"name": "lookup", "args": map[string]any{"a": 1}},
			}}, "", nil),
			streamCandidate([]map[string]any{{
				"functionCall": map[string]any{"name": "clock", "args": map[string]any{}},
			}}, "STOP", nil),
		)
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := assembleEvents(t, collectStream(t, stream))
	calls := assembled.Message.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].CallID != "call_0" || calls[0].Name != "lookup" {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[1].CallID != "call_1" || calls[1].Name != "clock" {
		t.Errorf("second call = %+v", calls[1])
	}
}

func TestStream_CumulativeToolCallSnapshot(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			streamCandidate([]map[string]any{{
				"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{}},
			}}, "", nil),
			streamCandidate([]map[string]any{{
				"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{"q": "go"}},
			}}, "STOP", nil),
		)
	})
	stream, err := m.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collectStream(t, stream)
	var snapshots int
	for _, event := range events {
		delta, ok := event.(llm.PartDelta)
		if !ok || delta.Call == nil {
			continue
		}
		if delta.Replace && string(delta.Call.InputSnapshot) == `{"q":"go"}` {
			snapshots++
		}
	}
	if snapshots != 1 {
		t.Errorf("tool call snapshots = %d, want 1", snapshots)
	}
	assembled := assembleEvents(t, events)
	calls := assembled.Message.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if string(calls[0].Input) != `{"q":"go"}` {
		t.Errorf("input = %s", calls[0].Input)
	}
}

func TestStream_ContextCancellation(t *testing.T) {
	m := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]}}]}`+"\n\n")
		flusher.Flush()
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := m.Stream(ctx, &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		cancel()
		t.Fatalf("Stream: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		cancel()
		t.Fatalf("first Recv: %v", err)
	}
	cancel()
	for {
		_, recvErr := stream.Recv()
		if recvErr == nil {
			continue
		}
		if !errors.Is(recvErr, context.Canceled) {
			if providerErr, ok := llm.AsError(recvErr); !ok || providerErr.Kind != llm.KindCanceled {
				t.Fatalf("Recv err = %v, want context.Canceled or KindCanceled", recvErr)
			}
		}
		break
	}
	_ = stream.Close()
}
