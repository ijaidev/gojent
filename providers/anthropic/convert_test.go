package anthropic

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ijaidev/gojent/core/llm"
)

func intPtr(value int) *int           { return &value }
func floatPtr(value float64) *float64 { return &value }
func boolPtr(value bool) *bool        { return &value }

// thinkingSignatureFixture is a realistic Anthropic thinking signature. It
// contains base64 padding and URL-unsafe characters so replay proves the value
// survives JSON encoding unchanged.
const thinkingSignatureFixture = "EqQBCkgIAxABGAIiQL+Z8f/2m0oPq3Xr7wT1vY4uJ5cK6bN8dF9gH0iL2mM3nO4pQ5rS6tU7vW8xY9zA=="

// TestRequestParamsGolden locks the on-the-wire request shape, including
// system hoisting, thinking signatures, tool schema keywords, and tool results.
func TestRequestParamsGolden(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}

	thinking := llm.NewReasoningPart("weigh options")
	if err := attachState(thinking, partState{Signature: thinkingSignatureFixture}); err != nil {
		t.Fatalf("attach thinking state: %v", err)
	}
	result := llm.NewToolResultPart(llm.ToolResult{
		CallID:     "toolu_1",
		Name:       "lookup",
		Kind:       llm.ToolKindFunction,
		Status:     llm.ToolStatusCompleted,
		Content:    []llm.Part{llm.NewTextPart("42")},
		Structured: json.RawMessage(`{"answer":42}`),
	})

	request := &llm.Request{
		Messages: []llm.Message{
			llm.SystemMessage("follow the rules"),
			llm.DeveloperMessage("be terse"),
			llm.UserMessageWithParts(
				llm.NewTextPart("look"),
				llm.FileFromBytes([]byte("png-bytes"), "image/png"),
			),
			{
				Role: llm.RoleAssistant,
				Content: []llm.Part{
					thinking,
					llm.NewTextPart("calling"),
					llm.NewToolCallPart(llm.ToolCall{
						CallID: "toolu_1",
						Name:   "lookup",
						Kind:   llm.ToolKindFunction,
						Input:  json.RawMessage(`{"q":"go"}`),
						Status: llm.ToolStatusCompleted,
					}),
				},
			},
			llm.UserMessageWithParts(result),
		},
		Tools: []llm.ToolDefinition{{
			Name:        "lookup",
			Description: "find a thing",
			Strict:      true,
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"q": map[string]any{"type": "string"}},
				"required":             []any{"q"},
				"additionalProperties": false,
			},
		}},
		ToolChoice:  llm.ToolChoice{Mode: llm.ToolChoiceRequired},
		Reasoning:   &llm.ReasoningConfig{Level: llm.ReasoningLevelHigh, IncludeThoughts: boolPtr(false)},
		Temperature: floatPtr(1),
		MaxTokens:   intPtr(16000),
		Stop:        []string{"END"},
		State:       llm.State{Mode: llm.StateModeReplay},
	}

	params, err := model.requestParams(request)
	if err != nil {
		t.Fatalf("requestParams: %v", err)
	}
	encoded := mustJSON(t, params)
	pretty := indentJSON(t, encoded)
	assertGolden(t, "request.golden.json", pretty)
}

func TestConvertMessagesHoistsSystem(t *testing.T) {
	messages, system, err := convertMessages([]llm.Message{
		llm.SystemMessage("system one"),
		llm.DeveloperMessage("developer two"),
		llm.UserMessage("hello"),
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(system) != 2 || system[0].Text != "system one" || system[1].Text != "developer two" {
		t.Fatalf("system = %+v", system)
	}
	if len(messages) != 1 || messages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestConvertMessagesThinkingSignatureReplay(t *testing.T) {
	thinking := llm.NewReasoningPart("considering")
	if err := attachState(thinking, partState{Signature: thinkingSignatureFixture}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	messages, _, err := convertMessages([]llm.Message{{
		Role:    llm.RoleAssistant,
		Content: []llm.Part{thinking},
	}})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
	block := messages[0].Content[0]
	if block.OfThinking == nil {
		t.Fatalf("block = %+v, want thinking", block)
	}
	if block.OfThinking.Thinking != "considering" || block.OfThinking.Signature != thinkingSignatureFixture {
		t.Fatalf("thinking = %+v", block.OfThinking)
	}

	// The signature must survive request serialization byte for byte.
	encoded, err := json.Marshal(block.OfThinking)
	if err != nil {
		t.Fatalf("marshal thinking: %v", err)
	}
	var wire struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode thinking: %v", err)
	}
	if wire.Signature != thinkingSignatureFixture {
		t.Fatalf("signature = %q, want %q", wire.Signature, thinkingSignatureFixture)
	}
}

func TestConvertMessagesToolResultsStayInUserTurns(t *testing.T) {
	call := llm.NewToolCallPart(llm.ToolCall{
		CallID: "call-1",
		Name:   "lookup",
		Kind:   llm.ToolKindFunction,
		Input:  json.RawMessage(`{}`),
	})
	result := llm.NewToolResultPart(llm.ToolResult{
		CallID:  "call-1",
		Name:    "lookup",
		Kind:    llm.ToolKindFunction,
		Status:  llm.ToolStatusError,
		Content: []llm.Part{llm.NewTextPart("boom")},
	})
	messages, _, err := convertMessages([]llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.Part{call}},
		llm.UserMessageWithParts(result),
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	block := messages[1].Content[0]
	if block.OfToolResult == nil {
		t.Fatalf("block = %+v, want tool_result", block)
	}
	if block.OfToolResult.ToolUseID != "call-1" || !block.OfToolResult.IsError.Valid() || !block.OfToolResult.IsError.Value {
		t.Fatalf("tool result = %+v", block.OfToolResult)
	}
}

func TestConvertFileRejectsAudioAndVideo(t *testing.T) {
	for _, mimeType := range []string{"audio/mpeg", "video/mp4"} {
		_, err := convertFileBlock(llm.FileFromBytes([]byte("x"), mimeType))
		if !errors.Is(err, llm.ErrUnsupportedPartType) {
			t.Errorf("%s: err = %v, want ErrUnsupportedPartType", mimeType, err)
		}
	}
}

func TestRequestStateRequiresReplay(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	store := true
	request := &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
		State:    llm.State{Mode: llm.StateModeServer, Store: &store},
	}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestRequestRejectsResponseFormat(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	request := &llm.Request{
		Messages:       []llm.Message{llm.UserMessage("hi")},
		ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSON},
	}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestRequestToolChoice(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	base := func(choice llm.ToolChoice) *llm.Request {
		return &llm.Request{
			Messages: []llm.Message{llm.UserMessage("hi")},
			Tools: []llm.ToolDefinition{
				{Name: "lookup", Parameters: map[string]any{"type": "object"}},
			},
			ToolChoice: choice,
		}
	}

	params, err := model.requestParams(base(llm.ToolChoice{Name: "lookup"}))
	if err != nil {
		t.Fatalf("requestParams: %v", err)
	}
	if params.ToolChoice.OfTool == nil || params.ToolChoice.OfTool.Name != "lookup" {
		t.Fatalf("tool choice = %+v", params.ToolChoice)
	}

	if _, err := model.requestParams(base(llm.ToolChoice{Mode: llm.ToolChoiceRequired})); err != nil {
		t.Fatalf("required: %v", err)
	}
	if _, err := model.requestParams(&llm.Request{
		Messages:   []llm.Message{llm.UserMessage("hi")},
		ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceRequired},
	}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("tool choice without tools err = %v, want ErrUnsupported", err)
	}
}

func TestToolInputSchemaPreservesKeywords(t *testing.T) {
	schema, err := toolInputSchema(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"q": map[string]any{"type": "string"}},
		"required":             []any{"q"},
		"additionalProperties": false,
		"$defs":                map[string]any{"x": map[string]any{"type": "number"}},
	})
	if err != nil {
		t.Fatalf("toolInputSchema: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("type = %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "q" {
		t.Fatalf("required = %v", schema.Required)
	}
	if schema.ExtraFields["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %v", schema.ExtraFields["additionalProperties"])
	}
	if _, ok := schema.ExtraFields["$defs"]; !ok {
		t.Fatalf("$defs missing from extras: %v", schema.ExtraFields)
	}
}

func TestThinkingBudgetValidation(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	request := &llm.Request{
		Messages:  []llm.Message{llm.UserMessage("hi")},
		Reasoning: &llm.ReasoningConfig{BudgetTokens: intPtr(10)},
	}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("small budget err = %v, want ErrUnsupported", err)
	}

	request.Reasoning = &llm.ReasoningConfig{BudgetTokens: intPtr(5000)}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("large budget err = %v, want ErrUnsupported", err)
	}

	request.Reasoning = &llm.ReasoningConfig{Level: llm.ReasoningLevelHigh}
	params, err := model.requestParams(request)
	if err != nil {
		t.Fatalf("high level: %v", err)
	}
	if params.Thinking.OfEnabled == nil || params.Thinking.OfEnabled.BudgetTokens != 4095 {
		t.Fatalf("thinking = %+v, want clamped budget 4095", params.Thinking)
	}
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

func TestToolResultRejectsInputRequests(t *testing.T) {
	_, err := convertToolResultBlock(&llm.ToolResult{
		CallID:        "call-1",
		Status:        llm.ToolStatusCompleted,
		InputRequests: json.RawMessage(`{"question":"which file?"}`),
	})
	if !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestToolResultRejectsServerKind(t *testing.T) {
	_, err := convertToolResultBlock(&llm.ToolResult{
		CallID: "call-1",
		Kind:   llm.ToolKindServer,
		Status: llm.ToolStatusCompleted,
	})
	if !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestReasoningNoneRejectsBudget(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	request := &llm.Request{
		Messages:  []llm.Message{llm.UserMessage("hi")},
		Reasoning: &llm.ReasoningConfig{Level: llm.ReasoningLevelNone, BudgetTokens: intPtr(2048)},
	}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestRequestRejectsHostedToolDefinitions(t *testing.T) {
	model := &Model{modelID: "claude-test", maxTokens: DefaultMaxTokens}
	request := &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
		Tools: []llm.ToolDefinition{{
			Name: "web_search",
			Kind: llm.ToolKindServer,
		}},
	}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
