package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

func TestProviderState_RoundTrip(t *testing.T) {
	part := llm.NewTextPart("hello")
	state, err := providerState(part)
	if err != nil {
		t.Fatalf("providerState(no metadata): %v", err)
	}
	if len(state.ThoughtSignature) != 0 {
		t.Errorf("unexpected state: %+v", state)
	}

	signature := []byte{0x01, 0x02, 0x03}
	if err := attachProviderState(part, partProviderState{ThoughtSignature: signature}); err != nil {
		t.Fatalf("attachProviderState: %v", err)
	}
	state, err = providerState(part)
	if err != nil {
		t.Fatalf("providerState: %v", err)
	}
	if !bytes.Equal(state.ThoughtSignature, signature) {
		t.Errorf("signature = %v, want %v", state.ThoughtSignature, signature)
	}
}

func TestProviderState_MalformedFailsClosed(t *testing.T) {
	part := llm.NewTextPart("hello")
	part.Provider = llm.ProviderData{providerName: json.RawMessage("{")}
	if _, err := providerState(part); err == nil {
		t.Fatal("expected malformed provider data to fail")
	}
}

func TestProviderState_EmptySignatureIsNotAttached(t *testing.T) {
	part := llm.NewTextPart("hello")
	if err := attachProviderState(part, partProviderState{}); err != nil {
		t.Fatalf("attachProviderState: %v", err)
	}
	if part.Provider != nil {
		t.Errorf("provider data = %+v, want nil", part.Provider)
	}
}

func TestConvertMessages_ReplaysThoughtSignatures(t *testing.T) {
	reasoning := llm.NewReasoningPart("think")
	if err := attachProviderState(reasoning, partProviderState{ThoughtSignature: []byte("rsig")}); err != nil {
		t.Fatalf("attach reasoning: %v", err)
	}
	text := llm.NewTextPart("answer")
	if err := attachProviderState(text, partProviderState{ThoughtSignature: []byte("tsig")}); err != nil {
		t.Fatalf("attach text: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		CallID: "call-1",
		Name:   "lookup",
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := attachProviderState(call, partProviderState{ThoughtSignature: []byte("csig")}); err != nil {
		t.Fatalf("attach call: %v", err)
	}
	assistant := llm.AssistantMessageWithParts(reasoning, text, call)
	results := llm.ToolResultsMessage(llm.ToolResult{
		CallID:  "call-1",
		Name:    "lookup",
		Content: []llm.Part{llm.NewTextPart(`{"ok":true}`)},
	})

	contents, system, err := convertMessages([]llm.Message{
		llm.UserMessage("find it"),
		assistant,
		results,
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if system != nil {
		t.Errorf("unexpected system instruction: %+v", system)
	}
	if len(contents) != 3 {
		t.Fatalf("contents = %d, want user, model, tool response", len(contents))
	}

	modelTurn := contents[1]
	if modelTurn.Role != genai.RoleModel {
		t.Errorf("assistant role = %q", modelTurn.Role)
	}
	if len(modelTurn.Parts) != 3 {
		t.Fatalf("assistant parts = %d, want 3", len(modelTurn.Parts))
	}
	if !modelTurn.Parts[0].Thought || string(modelTurn.Parts[0].ThoughtSignature) != "rsig" {
		t.Errorf("reasoning part = %+v", modelTurn.Parts[0])
	}
	if modelTurn.Parts[1].Text != "answer" || string(modelTurn.Parts[1].ThoughtSignature) != "tsig" {
		t.Errorf("text part = %+v", modelTurn.Parts[1])
	}
	if modelTurn.Parts[2].FunctionCall == nil ||
		modelTurn.Parts[2].FunctionCall.ID != "call-1" ||
		string(modelTurn.Parts[2].ThoughtSignature) != "csig" {
		t.Errorf("call part = %+v", modelTurn.Parts[2])
	}

	toolTurn := contents[2]
	if toolTurn.Role != genai.RoleUser {
		t.Errorf("tool turn role = %q, want user", toolTurn.Role)
	}
	response := toolTurn.Parts[0].FunctionResponse
	if response == nil || response.Name != "lookup" || response.ID != "call-1" {
		t.Fatalf("function response = %+v", response)
	}
	if response.Response["output"] != `{"ok":true}` {
		t.Errorf("function response body = %+v", response.Response)
	}
}

func TestConvertMessages_SignaturesSurviveJSONRoundTrip(t *testing.T) {
	text := llm.NewTextPart("answer")
	if err := attachProviderState(text, partProviderState{ThoughtSignature: []byte("tsig")}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		CallID: "call-1",
		Name:   "lookup",
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := attachProviderState(call, partProviderState{ThoughtSignature: []byte("csig")}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	assistant := llm.AssistantMessageWithParts(text, call)

	encoded, err := llm.MarshalMessage(assistant)
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	decoded, err := llm.UnmarshalMessage(encoded)
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}

	contents, _, err := convertMessages([]llm.Message{llm.UserMessage("find it"), decoded})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	parts := contents[1].Parts
	if string(parts[0].ThoughtSignature) != "tsig" {
		t.Errorf("text signature = %q", parts[0].ThoughtSignature)
	}
	if string(parts[1].ThoughtSignature) != "csig" {
		t.Errorf("call signature = %q", parts[1].ThoughtSignature)
	}
}

func TestConvertMessages_SystemInstruction(t *testing.T) {
	contents, system, err := convertMessages([]llm.Message{
		llm.SystemMessage("be terse"),
		llm.DeveloperMessage("answer in English"),
		llm.UserMessage("hi"),
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if system == nil || len(system.Parts) != 2 {
		t.Fatalf("system instruction = %+v", system)
	}
	if system.Parts[0].Text != "be terse" || system.Parts[1].Text != "answer in English" {
		t.Errorf("system parts = %+v", system.Parts)
	}
	if len(contents) != 1 || contents[0].Role != genai.RoleUser {
		t.Errorf("contents = %+v", contents)
	}
}

func TestConvertMessages_RejectsNonTextSystemParts(t *testing.T) {
	_, _, err := convertMessages([]llm.Message{{
		Role:    llm.RoleSystem,
		Content: []llm.Part{llm.FileFromBytes([]byte{1}, "image/png")},
	}})
	if !errors.Is(err, llm.ErrUnsupportedPartType) {
		t.Errorf("err = %v, want ErrUnsupportedPartType", err)
	}
}

func TestConvertMessages_RoleMappingAndToolGrouping(t *testing.T) {
	assistant := llm.AssistantMessageWithParts(
		llm.NewToolCallPart(llm.ToolCall{CallID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}),
		llm.NewToolCallPart(llm.ToolCall{CallID: "call-2", Name: "clock", Input: json.RawMessage(`{}`)}),
	)
	results := llm.ToolResultsMessage(
		llm.ToolResult{CallID: "call-1", Content: []llm.Part{llm.NewTextPart("sunny")}},
		llm.ToolResult{CallID: "call-2", Content: []llm.Part{llm.NewTextPart("noon")}},
	)
	contents, _, err := convertMessages([]llm.Message{
		llm.UserMessage("weather?"),
		assistant,
		results,
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(contents) != 3 {
		t.Fatalf("contents = %d", len(contents))
	}
	if contents[1].Role != genai.RoleModel {
		t.Errorf("assistant role = %q", contents[1].Role)
	}
	toolTurn := contents[2]
	if toolTurn.Role != genai.RoleUser {
		t.Errorf("tool turn role = %q", toolTurn.Role)
	}
	if len(toolTurn.Parts) != 2 {
		t.Fatalf("tool parts = %d, want 2", len(toolTurn.Parts))
	}
	for index, want := range []struct{ name, id string }{
		{name: "weather", id: "call-1"},
		{name: "clock", id: "call-2"},
	} {
		response := toolTurn.Parts[index].FunctionResponse
		if response == nil || response.Name != want.name || response.ID != want.id {
			t.Errorf("response %d = %+v, want %+v", index, response, want)
		}
	}
}

func TestCanonicalPart_GeneratedCallID(t *testing.T) {
	part, err := canonicalPart(&genai.Part{FunctionCall: &genai.FunctionCall{
		Name: "lookup",
		Args: map[string]any{"q": "go"},
	}}, 3)
	if err != nil {
		t.Fatalf("canonicalPart: %v", err)
	}
	call, ok := part.(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part = %T", part)
	}
	if call.CallID != "call_3" {
		t.Errorf("call id = %q, want call_3", call.CallID)
	}
	if call.ItemID != "" {
		t.Errorf("item id = %q, want empty", call.ItemID)
	}
	if string(call.Input) != `{"q":"go"}` {
		t.Errorf("input = %s", call.Input)
	}
}

func TestCanonicalPart_FileParts(t *testing.T) {
	inline, err := canonicalPart(&genai.Part{InlineData: &genai.Blob{
		MIMEType: "image/png",
		Data:     []byte{1, 2},
	}}, 0)
	if err != nil {
		t.Fatalf("canonicalPart inline: %v", err)
	}
	file, ok := inline.(*llm.FilePart)
	if !ok || file.MIMEType != "image/png" || !bytes.Equal(file.Data, []byte{1, 2}) {
		t.Errorf("inline = %+v", inline)
	}

	uri, err := canonicalPart(&genai.Part{FileData: &genai.FileData{
		MIMEType: "application/pdf",
		FileURI:  "gs://bucket/doc.pdf",
	}}, 0)
	if err != nil {
		t.Fatalf("canonicalPart file: %v", err)
	}
	file, ok = uri.(*llm.FilePart)
	if !ok || file.URL != "gs://bucket/doc.pdf" || file.MIMEType != "application/pdf" {
		t.Errorf("file = %+v", uri)
	}
}

func TestCanonicalPart_RejectsServerParts(t *testing.T) {
	tests := []struct {
		name string
		part *genai.Part
	}{
		{name: "function response", part: &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "x"}}},
		{name: "server tool call", part: &genai.Part{ToolCall: &genai.ToolCall{}}},
		{name: "server tool response", part: &genai.Part{ToolResponse: &genai.ToolResponse{}}},
		{name: "executable code", part: &genai.Part{ExecutableCode: &genai.ExecutableCode{}}},
		{name: "code execution result", part: &genai.Part{CodeExecutionResult: &genai.CodeExecutionResult{}}},
		{name: "audio transcription", part: &genai.Part{AudioTranscription: &genai.Transcription{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := canonicalPart(tc.part, 0); !errors.Is(err, llm.ErrUnsupportedPart) {
				t.Errorf("err = %v, want ErrUnsupportedPart", err)
			}
		})
	}
}

func TestToolResponseMap(t *testing.T) {
	t.Run("structured object", func(t *testing.T) {
		response, err := toolResponseMap(llm.ToolResult{
			CallID:     "call-1",
			Status:     llm.ToolStatusCompleted,
			Structured: json.RawMessage(`{"temp":20}`),
		})
		if err != nil {
			t.Fatalf("toolResponseMap: %v", err)
		}
		if response["temp"] != float64(20) {
			t.Errorf("response = %+v", response)
		}
		if response["output"] != "" {
			t.Errorf("output = %v, want empty", response["output"])
		}
	})

	t.Run("structured scalar", func(t *testing.T) {
		response, err := toolResponseMap(llm.ToolResult{
			CallID:     "call-1",
			Status:     llm.ToolStatusCompleted,
			Structured: json.RawMessage(`42`),
		})
		if err != nil {
			t.Fatalf("toolResponseMap: %v", err)
		}
		if response["output"] != float64(42) {
			t.Errorf("response = %+v", response)
		}
	})

	t.Run("text and error", func(t *testing.T) {
		response, err := toolResponseMap(llm.ToolResult{
			CallID:  "call-1",
			Status:  llm.ToolStatusError,
			Content: []llm.Part{llm.NewTextPart("boom")},
		})
		if err != nil {
			t.Fatalf("toolResponseMap: %v", err)
		}
		if response["output"] != "boom" || response["error"] != "boom" {
			t.Errorf("response = %+v", response)
		}
	})
}

func TestWrapError_Taxonomy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind llm.Kind
		code int
	}{
		{name: "pointer rate limit", err: &genai.APIError{Code: 429}, kind: llm.KindRateLimit, code: 429},
		{name: "value auth", err: genai.APIError{Code: 401}, kind: llm.KindAuth, code: 401},
		{name: "invalid", err: &genai.APIError{Code: 400}, kind: llm.KindInvalid, code: 400},
		{name: "overloaded", err: &genai.APIError{Code: 503}, kind: llm.KindOverloaded, code: 503},
		{name: "wrapped", err: &wrappedError{inner: &genai.APIError{Code: 429}}, kind: llm.KindRateLimit, code: 429},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := wrapError(tc.err)
			providerErr, ok := llm.AsError(wrapped)
			if !ok {
				t.Fatalf("wrapError returned %T, want *llm.Error", wrapped)
			}
			if providerErr.Kind != tc.kind || providerErr.StatusCode != tc.code {
				t.Errorf("error = %+v, want kind %s code %d", providerErr, tc.kind, tc.code)
			}
			var (
				valueErr   genai.APIError
				pointerErr *genai.APIError
			)
			if !errors.As(wrapped, &valueErr) && !errors.As(wrapped, &pointerErr) {
				t.Error("wrapped error does not unwrap to the provider error")
			}
		})
	}
	if wrapError(nil) != nil {
		t.Error("wrapError(nil) should be nil")
	}
}

type wrappedError struct {
	inner error
}

func (e *wrappedError) Error() string { return "wrapped: " + e.inner.Error() }
func (e *wrappedError) Unwrap() error { return e.inner }

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		in   genai.FinishReason
		want llm.FinishReason
	}{
		{in: genai.FinishReasonStop, want: llm.FinishReasonStop},
		{in: genai.FinishReasonMaxTokens, want: llm.FinishReasonLength},
		{in: genai.FinishReasonSafety, want: llm.FinishReasonContentFilter},
		{in: genai.FinishReasonBlocklist, want: llm.FinishReasonContentFilter},
		{in: genai.FinishReasonUnexpectedToolCall, want: llm.FinishReasonTool},
	}
	for _, tc := range tests {
		if got := mapFinishReason(tc.in); got != tc.want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestConvertPartToGemini_EdgeCases(t *testing.T) {
	summaryOnly := &llm.ReasoningPart{Summary: []string{"first", "second"}}
	emptyReasoning := &llm.ReasoningPart{}

	tests := []struct {
		name    string
		part    llm.Part
		wantErr error
		check   func(*testing.T, *genai.Part)
	}{
		{
			name:    "nil part",
			part:    nil,
			wantErr: llm.ErrInvalidPart,
		},
		{
			name: "empty text is skipped",
			part: llm.NewTextPart(""),
			check: func(t *testing.T, part *genai.Part) {
				if part != nil {
					t.Errorf("part = %+v, want nil", part)
				}
			},
		},
		{
			name: "reasoning summary is joined",
			part: summaryOnly,
			check: func(t *testing.T, part *genai.Part) {
				if part.Text != "first\nsecond" || !part.Thought {
					t.Errorf("part = %+v", part)
				}
			},
		},
		{
			name:    "reasoning without text or signature",
			part:    emptyReasoning,
			wantErr: llm.ErrUnsupportedPart,
		},
		{
			name: "refusal replays as text",
			part: llm.NewRefusalPart("I cannot help with that"),
			check: func(t *testing.T, part *genai.Part) {
				if part.Text != "I cannot help with that" {
					t.Errorf("part = %+v", part)
				}
			},
		},
		{
			name:    "empty refusal",
			part:    llm.NewRefusalPart(""),
			wantErr: llm.ErrInvalidPart,
		},
		{
			name: "custom tool call",
			part: llm.NewToolCallPart(llm.ToolCall{
				CallID: "call-1",
				Name:   "shell",
				Kind:   llm.ToolKindCustom,
				Input:  json.RawMessage(`"ls"`),
			}),
			wantErr: llm.ErrUnsupported,
		},
		{
			name: "tool input must be an object",
			part: llm.NewToolCallPart(llm.ToolCall{
				CallID: "call-1",
				Name:   "lookup",
				Input:  json.RawMessage(`[1,2]`),
			}),
			wantErr: llm.ErrInvalidToolCall,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			part, err := convertPartToGemini(tc.part)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("convertPartToGemini: %v", err)
			}
			tc.check(t, part)
		})
	}
}

func TestConvertToolResultToGemini_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		result  llm.ToolResult
		tool    string
		wantErr error
		check   func(*testing.T, *genai.Part)
	}{
		{
			name: "server result",
			result: llm.ToolResult{
				CallID: "call-1",
				Name:   "search",
				Kind:   llm.ToolKindServer,
			},
			tool:    "search",
			wantErr: llm.ErrUnsupported,
		},
		{
			name: "unknown status",
			result: llm.ToolResult{
				CallID: "call-1",
				Name:   "lookup",
				Status: "queued",
			},
			tool:    "lookup",
			wantErr: llm.ErrUnsupported,
		},
		{
			name:    "missing call id",
			result:  llm.ToolResult{Name: "lookup"},
			tool:    "lookup",
			wantErr: llm.ErrInvalidToolResult,
		},
		{
			name:    "missing name",
			result:  llm.ToolResult{CallID: "call-1"},
			tool:    "",
			wantErr: llm.ErrInvalidToolResult,
		},
		{
			name: "file id cannot be replayed",
			result: llm.ToolResult{
				CallID:  "call-1",
				Name:    "lookup",
				Content: []llm.Part{llm.FileFromID("files/abc", "image/png")},
			},
			tool:    "lookup",
			wantErr: llm.ErrUnsupportedPart,
		},
		{
			name: "unsupported file MIME",
			result: llm.ToolResult{
				CallID:  "call-1",
				Name:    "lookup",
				Content: []llm.Part{llm.FileFromBytes([]byte{1}, "application/zip")},
			},
			tool:    "lookup",
			wantErr: llm.ErrUnsupportedMIMEType,
		},
		{
			name: "non-text content",
			result: llm.ToolResult{
				CallID:  "call-1",
				Name:    "lookup",
				Content: []llm.Part{llm.NewReasoningPart("thinking")},
			},
			tool:    "lookup",
			wantErr: llm.ErrInvalidToolResult,
		},
		{
			name: "URI content",
			result: llm.ToolResult{
				CallID:  "call-1",
				Name:    "lookup",
				Content: []llm.Part{llm.FileFromURL("gs://bucket/doc.pdf", "application/pdf")},
			},
			tool: "lookup",
			check: func(t *testing.T, part *genai.Part) {
				response := part.FunctionResponse
				if response == nil || len(response.Parts) != 1 {
					t.Fatalf("response = %+v", response)
				}
				file := response.Parts[0].FileData
				if file == nil || file.FileURI != "gs://bucket/doc.pdf" || file.MIMEType != "application/pdf" {
					t.Errorf("file data = %+v", file)
				}
			},
		},
		{
			name: "denied result sets error",
			result: llm.ToolResult{
				CallID:  "call-1",
				Name:    "lookup",
				Status:  llm.ToolStatusDenied,
				Content: []llm.Part{llm.NewTextPart("not allowed")},
			},
			tool: "lookup",
			check: func(t *testing.T, part *genai.Part) {
				response := part.FunctionResponse
				if response == nil || response.Response["error"] != "not allowed" {
					t.Errorf("response = %+v", response)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			part, err := convertToolResultToGemini(tc.result, tc.tool)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("convertToolResultToGemini: %v", err)
			}
			tc.check(t, part)
		})
	}
}

func TestMapGenaiLevelToLLM(t *testing.T) {
	tests := []struct {
		in   genai.ThinkingLevel
		want llm.ReasoningLevel
	}{
		{in: genai.ThinkingLevelUnspecified, want: ""},
		{in: genai.ThinkingLevelMinimal, want: llm.ReasoningLevelMinimal},
		{in: genai.ThinkingLevelLow, want: llm.ReasoningLevelLow},
		{in: genai.ThinkingLevelMedium, want: llm.ReasoningLevelMedium},
		{in: genai.ThinkingLevelHigh, want: llm.ReasoningLevelHigh},
		{in: genai.ThinkingLevel("EXTREME"), want: llm.ReasoningLevel("EXTREME")},
	}
	for _, tc := range tests {
		if got := mapGenaiLevelToLLM(tc.in); got != tc.want {
			t.Errorf("mapGenaiLevelToLLM(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWrapError_ContextBranches(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind llm.Kind
	}{
		{name: "deadline", err: context.DeadlineExceeded, kind: llm.KindTimeout},
		{name: "canceled", err: context.Canceled, kind: llm.KindCanceled},
		{name: "wrapped deadline", err: fmt.Errorf("request: %w", context.DeadlineExceeded), kind: llm.KindTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := wrapError(tc.err)
			providerErr, ok := llm.AsError(wrapped)
			if !ok {
				t.Fatalf("wrapError returned %T, want *llm.Error", wrapped)
			}
			if providerErr.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", providerErr.Kind, tc.kind)
			}
			if !errors.Is(wrapped, tc.err) {
				t.Error("wrapped error does not preserve the cause")
			}
		})
	}

	plain := errors.New("plain")
	if got := wrapError(plain); !errors.Is(got, plain) {
		t.Errorf("wrapError(plain) = %v, want the original error", got)
	}
}
