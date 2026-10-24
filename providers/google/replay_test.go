package google

import (
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

func TestProviderData_RoundTripPreservesThoughtSignature(t *testing.T) {
	original := &genai.Content{
		Role: genai.RoleModel,
		Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "lookup",
				Args: map[string]any{"query": "go"},
			},
			ThoughtSignature: []byte{0x01, 0x02, 0x03, 0x04},
		}},
	}

	data := providerDataFromContent(original)
	if data == nil || data.Provider != providerName || data.Version != 1 {
		t.Fatalf("unexpected provider data: %+v", data)
	}
	decoded := decodeProviderData(data)
	if decoded == nil {
		t.Fatal("failed to decode provider data")
	}
	if decoded.Role != genai.RoleModel {
		t.Errorf("role = %q", decoded.Role)
	}
	if len(decoded.Parts) != 1 {
		t.Fatalf("parts = %d", len(decoded.Parts))
	}
	part := decoded.Parts[0]
	if part.FunctionCall == nil || part.FunctionCall.ID != "call-1" || part.FunctionCall.Name != "lookup" {
		t.Errorf("function call lost: %+v", part.FunctionCall)
	}
	if string(part.ThoughtSignature) != string(original.Parts[0].ThoughtSignature) {
		t.Errorf("thought signature = %v, want %v", part.ThoughtSignature, original.Parts[0].ThoughtSignature)
	}
}

func TestProviderData_RejectsForeignOrEmptyData(t *testing.T) {
	if providerDataFromContent(nil) != nil {
		t.Error("nil content should produce nil provider data")
	}
	if providerDataFromContent(&genai.Content{}) != nil {
		t.Error("empty content should produce nil provider data")
	}
	foreign := &llm.ProviderData{Provider: "openai", Data: []byte(`{"x":1}`)}
	if decodeProviderData(foreign) != nil {
		t.Error("foreign provider data should be ignored")
	}
	if decodeProviderData(&llm.ProviderData{Provider: providerName, Data: []byte("{")}) != nil {
		t.Error("invalid JSON should be ignored")
	}
}

func TestConvertMessages_ReplaysProviderData(t *testing.T) {
	raw := &genai.Content{
		Role: genai.RoleModel,
		Parts: []*genai.Part{{
			FunctionCall:     &genai.FunctionCall{ID: "call-1", Name: "lookup", Args: map[string]any{"q": "go"}},
			ThoughtSignature: []byte("signature"),
		}},
	}
	messages := []llm.Message{
		llm.UserMessage("find it"),
		{
			Role:         llm.RoleAssistant,
			ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`}},
			ProviderData: providerDataFromContent(raw),
		},
		{
			Role:       llm.RoleTool,
			Name:       "lookup",
			ToolCallID: "call-1",
			Content:    []llm.Part{llm.TextPart(`{"answer":42}`)},
		},
	}

	contents, system, err := convertMessages(messages)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if system != nil {
		t.Errorf("unexpected system instruction: %+v", system)
	}
	if len(contents) != 3 {
		t.Fatalf("contents = %d, want user, model, tool response", len(contents))
	}
	assistant := contents[1]
	if assistant.Role != genai.RoleModel {
		t.Errorf("assistant role = %q", assistant.Role)
	}
	if len(assistant.Parts) != 1 || assistant.Parts[0].FunctionCall == nil {
		t.Fatalf("assistant parts = %+v", assistant.Parts)
	}
	if string(assistant.Parts[0].ThoughtSignature) != "signature" {
		t.Errorf("thought signature lost during replay: %v", assistant.Parts[0].ThoughtSignature)
	}
	toolTurn := contents[2]
	if toolTurn.Role != genai.RoleUser {
		t.Errorf("tool turn role = %q, want user", toolTurn.Role)
	}
	response := toolTurn.Parts[0].FunctionResponse
	if response == nil || response.Name != "lookup" || response.ID != "call-1" {
		t.Fatalf("function response = %+v", response)
	}
	if response.Response["answer"] != float64(42) {
		t.Errorf("function response payload = %+v", response.Response)
	}
}

func TestConvertMessages_SystemInstruction(t *testing.T) {
	contents, system, err := convertMessages([]llm.Message{
		llm.SystemMessage("be terse"),
		llm.UserMessage("hi"),
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if system == nil || len(system.Parts) != 1 || system.Parts[0].Text != "be terse" {
		t.Errorf("system instruction = %+v", system)
	}
	if len(contents) != 1 || contents[0].Role != genai.RoleUser {
		t.Errorf("contents = %+v", contents)
	}
}

func TestConvertMessages_RejectsNonTextSystemParts(t *testing.T) {
	_, _, err := convertMessages([]llm.Message{{
		Role:    llm.RoleSystem,
		Content: []llm.Part{llm.ImageFromBytes([]byte{1}, "image/png")},
	}})
	if !errors.Is(err, llm.ErrUnsupportedPartType) {
		t.Errorf("err = %v, want ErrUnsupportedPartType", err)
	}
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
			if _, ok := tc.err.(genai.APIError); !ok && !errors.Is(wrapped, tc.err) {
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

func TestConvertMessages_NilProviderDataFallsBackToCanonical(t *testing.T) {
	contents, _, err := convertMessages([]llm.Message{{
		Role:      llm.RoleAssistant,
		Content:   []llm.Part{llm.TextPart("canonical")},
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":"go"}`}},
	}})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents = %d", len(contents))
	}
	parts := contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want text and function call", len(parts))
	}
	if parts[0].Text != "canonical" {
		t.Errorf("text = %q", parts[0].Text)
	}
	if parts[1].FunctionCall == nil || parts[1].FunctionCall.Name != "lookup" {
		t.Errorf("function call = %+v", parts[1].FunctionCall)
	}
}
