package openai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/responses"
)

func intPtr(value int) *int           { return &value }
func floatPtr(value float64) *float64 { return &value }
func boolPtr(value bool) *bool        { return &value }

// providerData builds adapter-specific metadata for a tool definition.
func providerData(t *testing.T, value any) llm.ProviderData {
	t.Helper()
	data := make(llm.ProviderData)
	if err := data.Set(providerName, value); err != nil {
		t.Fatalf("set provider data: %v", err)
	}
	return data
}

// TestRequestParamsGolden locks the on-the-wire Responses request shape:
// instructions hoisting, item ordering, reasoning replay, hosted call replay,
// tool definitions, tool choice, and structured output.
func TestRequestParamsGolden(t *testing.T) {
	reasoning := llm.NewReasoningPart("")
	reasoning.Summary = []string{"weigh options"}
	if err := attachState(reasoning, partState{
		ItemID:           "rs_1",
		EncryptedContent: "encrypted-1",
		Status:           "completed",
	}); err != nil {
		t.Fatalf("attach reasoning state: %v", err)
	}

	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "fc_1",
		CallID: "call_1",
		Name:   "lookup",
		Kind:   llm.ToolKindFunction,
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := attachState(call, partState{Status: "completed"}); err != nil {
		t.Fatalf("attach call state: %v", err)
	}

	hostedCall := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "ws_1",
		CallID: "ws_1",
		Name:   "web_search",
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage(`{}`),
	})
	if err := attachState(hostedCall, partState{
		ServerItem: mustJSON(t, webSearchItem("ws_1", "go", "completed")),
	}); err != nil {
		t.Fatalf("attach hosted state: %v", err)
	}

	result := llm.NewToolResultPart(llm.ToolResult{
		CallID:     "call_1",
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
			llm.AssistantMessageWithParts(
				reasoning,
				llm.NewTextPart("calling"),
				call,
				hostedCall,
			),
			llm.UserMessageWithParts(result),
		},
		Tools: []llm.ToolDefinition{
			{
				Name:        "lookup",
				Description: "find a thing",
				Strict:      true,
				Parameters: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"q": map[string]any{"type": "string"}},
					"required":             []any{"q"},
					"additionalProperties": false,
				},
			},
			{
				Name:       "notes",
				Kind:       llm.ToolKindMCP,
				Parameters: map[string]any{"type": "object"},
			},
			{
				Name:     "web_search",
				Kind:     llm.ToolKindServer,
				Provider: providerData(t, map[string]any{"type": "web_search_preview"}),
			},
		},
		ToolChoice: llm.ToolChoice{Name: "lookup"},
		Reasoning: &llm.ReasoningConfig{
			Level:           llm.ReasoningLevelHigh,
			IncludeThoughts: boolPtr(true),
		},
		ResponseFormat: &llm.ResponseFormat{
			Type:   llm.ResponseFormatJSONSchema,
			Name:   "answer",
			Strict: true,
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"answer": map[string]any{"type": "integer"}},
				"required":   []any{"answer"},
			},
		},
		Temperature: floatPtr(0.2),
		TopP:        floatPtr(0.9),
		MaxTokens:   intPtr(1024),
		State:       llm.State{Mode: llm.StateModeReplay},
	}

	params, err := (&Model{modelID: "gpt-test"}).requestParams(request)
	if err != nil {
		t.Fatalf("requestParams: %v", err)
	}
	assertGolden(t, "request.golden.json", indentJSON(t, mustJSON(t, params)))
}

func TestConvertInstructions(t *testing.T) {
	instructions, err := convertInstructions([]llm.Message{
		llm.SystemMessage("system one"),
		llm.DeveloperMessage("developer two"),
		llm.UserMessage("hello"),
	})
	if err != nil {
		t.Fatalf("convertInstructions: %v", err)
	}
	if instructions != "system one\n\ndeveloper two" {
		t.Fatalf("instructions = %q", instructions)
	}

	multi := llm.Message{
		Role: llm.RoleSystem,
		Content: []llm.Part{
			llm.NewTextPart("line one"),
			llm.NewTextPart("line two"),
		},
	}
	instructions, err = convertInstructions([]llm.Message{multi})
	if err != nil {
		t.Fatalf("convertInstructions multi: %v", err)
	}
	if instructions != "line one\nline two" {
		t.Fatalf("instructions = %q", instructions)
	}
}

func TestConvertInstructionsRejectsNameAndPhase(t *testing.T) {
	system := llm.SystemMessage("rules")
	system.Name = "policy"
	if _, err := convertInstructions([]llm.Message{system}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("system name err = %v, want ErrUnsupported", err)
	}

	user := llm.UserMessage("hi")
	user.Phase = "commentary"
	if _, err := convertInput([]llm.Message{user}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("user phase err = %v, want ErrUnsupported", err)
	}

	system = llm.SystemMessage("rules")
	system.Phase = "final_answer"
	if _, err := convertInstructions([]llm.Message{system}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("system phase err = %v, want ErrUnsupported", err)
	}

	user = llm.UserMessage("hi")
	user.Name = "alice"
	if _, err := convertInput([]llm.Message{user}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("user name err = %v, want ErrUnsupported", err)
	}

	assistant := llm.AssistantMessage("hi")
	assistant.Name = "assistant"
	if _, err := convertInput([]llm.Message{assistant}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("assistant name err = %v, want ErrUnsupported", err)
	}

	if _, err := convertInstructions([]llm.Message{llm.UserMessageWithParts(
		llm.FileFromBytes([]byte("x"), "image/png"),
	)}); err != nil {
		t.Fatalf("non-instruction message should be skipped: %v", err)
	}
}

func TestConvertInstructionsRejectsNonText(t *testing.T) {
	system := llm.Message{
		Role:    llm.RoleSystem,
		Content: []llm.Part{llm.FileFromBytes([]byte("x"), "image/png")},
	}
	if _, err := convertInstructions([]llm.Message{system}); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("err = %v, want ErrUnsupportedPart", err)
	}
}

func TestConvertUserMessageFiles(t *testing.T) {
	message := llm.UserMessageWithParts(
		llm.NewTextPart("describe"),
		llm.FileFromBytes([]byte("png-bytes"), "image/png"),
		llm.FileFromBytes([]byte("%PDF"), "application/pdf"),
	)
	items, err := convertUserMessage(&message)
	if err != nil {
		t.Fatalf("convertUserMessage: %v", err)
	}
	if len(items) != 1 || items[0].OfMessage == nil {
		t.Fatalf("items = %+v", items)
	}
	content := items[0].OfMessage.Content.OfInputItemContentList
	if len(content) != 3 {
		t.Fatalf("content = %+v", content)
	}
	if content[0].OfInputText == nil || content[0].OfInputText.Text != "describe" {
		t.Fatalf("text = %+v", content[0])
	}
	if content[1].OfInputImage == nil {
		t.Fatalf("image = %+v", content[1])
	}
	if got := content[1].OfInputImage.ImageURL.Value; got != "data:image/png;base64,cG5nLWJ5dGVz" {
		t.Fatalf("image url = %q", got)
	}
	if content[2].OfInputFile == nil {
		t.Fatalf("file = %+v", content[2])
	}
	if got := content[2].OfInputFile.FileData.Value; got != "data:application/pdf;base64,JVBERg==" {
		t.Fatalf("file data = %q", got)
	}
}

func TestConvertUserMessageFlushesAroundToolResults(t *testing.T) {
	message := llm.UserMessageWithParts(
		llm.NewTextPart("before"),
		llm.NewToolResultPart(llm.ToolResult{
			CallID:  "call_1",
			Name:    "lookup",
			Status:  llm.ToolStatusCompleted,
			Content: []llm.Part{llm.NewTextPart("42")},
		}),
		llm.NewTextPart("after"),
	)
	items, err := convertUserMessage(&message)
	if err != nil {
		t.Fatalf("convertUserMessage: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want message, result, message", len(items))
	}
	if items[0].OfMessage == nil || items[1].OfFunctionCallOutput == nil || items[2].OfMessage == nil {
		t.Fatalf("items = %+v", items)
	}
	if items[1].OfFunctionCallOutput.CallID != "call_1" || items[1].OfFunctionCallOutput.Output != "42" {
		t.Fatalf("result = %+v", items[1].OfFunctionCallOutput)
	}
}

func TestConvertInputRejectsAudioAndVideo(t *testing.T) {
	for _, mimeType := range []string{"audio/mpeg", "video/mp4"} {
		message := llm.UserMessageWithParts(llm.FileFromBytes([]byte("x"), mimeType))
		_, err := convertUserMessage(&message)
		if !errors.Is(err, llm.ErrUnsupportedPartType) {
			t.Errorf("%s: err = %v, want ErrUnsupportedPartType", mimeType, err)
		}
	}
}

func TestConvertAssistantReplaysReasoningAndCall(t *testing.T) {
	reasoning := llm.NewReasoningPart("")
	reasoning.Summary = []string{"thinking"}
	if err := attachState(reasoning, partState{
		ItemID:           "rs_1",
		EncryptedContent: "encrypted-1",
		Status:           "completed",
	}); err != nil {
		t.Fatalf("attach reasoning state: %v", err)
	}
	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "fc_1",
		CallID: "call_1",
		Name:   "lookup",
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := attachState(call, partState{Status: "in_progress"}); err != nil {
		t.Fatalf("attach call state: %v", err)
	}
	message := llm.AssistantMessageWithParts(
		reasoning,
		llm.NewTextPart("calling"),
		call,
	)

	items, err := convertAssistantMessage(&message)
	if err != nil {
		t.Fatalf("convertAssistantMessage: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want reasoning, message, call", len(items))
	}

	item := items[0].OfReasoning
	if item == nil {
		t.Fatalf("item 0 = %+v", items[0])
	}
	if item.ID != "rs_1" || item.EncryptedContent.Value != "encrypted-1" {
		t.Fatalf("reasoning = %+v", item)
	}
	if len(item.Summary) != 1 || item.Summary[0].Text != "thinking" {
		t.Fatalf("summary = %+v", item.Summary)
	}
	if item.Status != responses.ResponseReasoningItemStatusCompleted {
		t.Fatalf("reasoning status = %q", item.Status)
	}

	if items[1].OfMessage == nil {
		t.Fatalf("item 1 = %+v", items[1])
	}
	content := items[1].OfMessage.Content.OfInputItemContentList
	if len(content) != 1 || content[0].OfInputText == nil || content[0].OfInputText.Text != "calling" {
		t.Fatalf("message content = %+v", content)
	}

	function := items[2].OfFunctionCall
	if function == nil {
		t.Fatalf("item 2 = %+v", items[2])
	}
	if function.ID.Value != "fc_1" || function.CallID != "call_1" || function.Name != "lookup" {
		t.Fatalf("call = %+v", function)
	}
	if function.Arguments != `{"q":"go"}` {
		t.Fatalf("arguments = %q", function.Arguments)
	}
	if function.Status != responses.ResponseFunctionToolCallStatusInProgress {
		t.Fatalf("status = %q", function.Status)
	}
}

func TestConvertAssistantReasoningRequiresState(t *testing.T) {
	message := llm.AssistantMessageWithParts(llm.NewReasoningPart(""))

	if _, err := convertAssistantMessage(&message); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("err = %v, want ErrUnsupportedPart", err)
	}

	text := llm.AssistantMessageWithParts(llm.NewReasoningPart("free-form"))
	if _, err := convertAssistantMessage(&text); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("text err = %v, want ErrUnsupportedPart", err)
	}
}

func TestConvertAssistantToolCallKind(t *testing.T) {
	server := llm.NewToolCallPart(llm.ToolCall{
		CallID: "server_1",
		Name:   "web_search",
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage(`{}`),
	})
	if _, err := convertAssistantMessage(&llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.Part{server},
	}); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("missing recorded item err = %v, want ErrUnsupportedPart", err)
	}

	custom := llm.NewToolCallPart(llm.ToolCall{
		CallID: "custom_1",
		Name:   "shell",
		Kind:   llm.ToolKindCustom,
		Input:  json.RawMessage(`"ls"`),
	})
	if _, err := convertAssistantMessage(&llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.Part{custom},
	}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("custom call err = %v, want ErrUnsupported", err)
	}
}

func TestInputServerCallReplaysRecordedItem(t *testing.T) {
	call := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "ws_1",
		CallID: "ws_1",
		Name:   "web_search",
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage(`{}`),
	})
	raw := mustJSON(t, webSearchItem("ws_1", "go", "completed"))
	if err := attachState(call, partState{ServerItem: raw}); err != nil {
		t.Fatalf("attach state: %v", err)
	}

	item, err := inputServerCall(call)
	if err != nil {
		t.Fatalf("inputServerCall: %v", err)
	}
	if item.OfWebSearchCall == nil {
		t.Fatalf("item = %+v, want web_search_call", item)
	}
	if item.OfWebSearchCall.ID != "ws_1" {
		t.Fatalf("id = %q", item.OfWebSearchCall.ID)
	}

	if _, err := inputServerCall(llm.NewToolCallPart(llm.ToolCall{
		CallID: "ws_2",
		Name:   "web_search",
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage(`{}`),
	})); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("missing state err = %v, want ErrUnsupportedPart", err)
	}

	invalid := llm.NewToolCallPart(llm.ToolCall{
		CallID: "call_1",
		Name:   "lookup",
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage(`{}`),
	})
	if err := attachState(invalid, partState{
		ServerItem: mustJSON(t, functionCallItem("fc_1", "call_1", "lookup", `{}`, "completed")),
	}); err != nil {
		t.Fatalf("attach invalid state: %v", err)
	}
	if _, err := inputServerCall(invalid); !errors.Is(err, llm.ErrUnsupportedPart) {
		t.Fatalf("non-hosted item err = %v, want ErrUnsupportedPart", err)
	}
}

func TestInputToolResult(t *testing.T) {
	base := func() *llm.ToolResult {
		return &llm.ToolResult{
			CallID:     "call_1",
			Name:       "lookup",
			Kind:       llm.ToolKindFunction,
			Status:     llm.ToolStatusCompleted,
			Content:    []llm.Part{llm.NewTextPart("42")},
			Structured: json.RawMessage(`{"answer":42}`),
		}
	}

	item, err := inputToolResult(base())
	if err != nil {
		t.Fatalf("inputToolResult: %v", err)
	}
	if item.OfFunctionCallOutput == nil {
		t.Fatalf("item = %+v", item)
	}
	if item.OfFunctionCallOutput.CallID != "call_1" {
		t.Fatalf("call id = %q", item.OfFunctionCallOutput.CallID)
	}
	if item.OfFunctionCallOutput.Output != "42\n{\"answer\":42}" {
		t.Fatalf("output = %q", item.OfFunctionCallOutput.Output)
	}

	for _, status := range []llm.ToolStatus{llm.ToolStatusInProgress, llm.ToolStatusIncomplete} {
		result := base()
		result.Status = status
		item, err := inputToolResult(result)
		if err != nil {
			t.Fatalf("%s: inputToolResult: %v", status, err)
		}
		if item.OfFunctionCallOutput.Status != string(status) {
			t.Errorf("%s: status = %q", status, item.OfFunctionCallOutput.Status)
		}
	}

	for _, status := range []llm.ToolStatus{llm.ToolStatusError, llm.ToolStatusDenied} {
		result := base()
		result.Status = status
		item, err := inputToolResult(result)
		if err != nil {
			t.Fatalf("%s: inputToolResult: %v", status, err)
		}
		if item.OfFunctionCallOutput.Status != "" {
			t.Errorf("%s: status = %q, want empty", status, item.OfFunctionCallOutput.Status)
		}
	}
}

func TestInputToolResultRejectsUnsupportedContent(t *testing.T) {
	result := &llm.ToolResult{
		CallID:  "call_1",
		Status:  llm.ToolStatusCompleted,
		Content: []llm.Part{llm.FileFromBytes([]byte("x"), "image/png")},
	}
	if _, err := inputToolResult(result); !errors.Is(err, llm.ErrUnsupportedPartType) {
		t.Fatalf("file err = %v, want ErrUnsupportedPartType", err)
	}

	result = &llm.ToolResult{
		CallID:        "call_1",
		Status:        llm.ToolStatusCompleted,
		InputRequests: json.RawMessage(`{"question":"which file?"}`),
	}
	if _, err := inputToolResult(result); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("input requests err = %v, want ErrUnsupported", err)
	}

	result = &llm.ToolResult{
		CallID: "call_1",
		Kind:   llm.ToolKindServer,
		Status: llm.ToolStatusCompleted,
	}
	if _, err := inputToolResult(result); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("server err = %v, want ErrUnsupported", err)
	}

	if _, err := inputToolResult(&llm.ToolResult{Status: llm.ToolStatusCompleted}); !errors.Is(err, llm.ErrInvalidToolResult) {
		t.Fatalf("missing call id err = %v, want ErrInvalidToolResult", err)
	}
}

func TestRequestState(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	base := func(state llm.State) *llm.Request {
		return &llm.Request{
			Messages: []llm.Message{llm.UserMessage("hi")},
			State:    state,
		}
	}

	params, err := model.requestParams(base(llm.State{
		Mode:       llm.StateModeServer,
		PreviousID: "resp_0",
		Store:      boolPtr(false),
	}))
	if err != nil {
		t.Fatalf("server state: %v", err)
	}
	if params.PreviousResponseID.Value != "resp_0" || !params.PreviousResponseID.Valid() {
		t.Fatalf("previous id = %+v", params.PreviousResponseID)
	}
	if !params.Store.Valid() || params.Store.Value {
		t.Fatalf("store = %+v, want explicit false", params.Store)
	}

	params, err = model.requestParams(base(llm.State{Mode: llm.StateModeServer}))
	if err != nil {
		t.Fatalf("server state default: %v", err)
	}
	if !params.Store.Valid() || !params.Store.Value {
		t.Fatalf("store = %+v, want default true", params.Store)
	}

	params, err = model.requestParams(base(llm.State{}))
	if err != nil {
		t.Fatalf("replay state: %v", err)
	}
	if params.PreviousResponseID.Valid() {
		t.Fatalf("replay sent previous id: %+v", params.PreviousResponseID)
	}

	params, err = model.requestParams(base(llm.State{Store: boolPtr(true)}))
	if err != nil {
		t.Fatalf("replay store: %v", err)
	}
	if !params.Store.Valid() || !params.Store.Value {
		t.Fatalf("replay store = %+v", params.Store)
	}

	if _, err := model.requestParams(base(llm.State{
		Mode:       llm.StateModeReplay,
		PreviousID: "resp_0",
	})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("replay previous err = %v, want ErrUnsupported", err)
	}

	state := llm.State{Provider: providerData(t, map[string]any{"foo": "bar"})}
	if _, err := model.requestParams(base(state)); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("provider state err = %v, want ErrUnsupported", err)
	}
}

func TestReasoningParam(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	base := func(config *llm.ReasoningConfig) *llm.Request {
		return &llm.Request{
			Messages:  []llm.Message{llm.UserMessage("hi")},
			Reasoning: config,
		}
	}

	tests := []struct {
		name       string
		config     *llm.ReasoningConfig
		effort     string
		summary    string
		configured bool
	}{
		{name: "empty", config: nil},
		{name: "minimal", config: &llm.ReasoningConfig{Level: llm.ReasoningLevelMinimal}, effort: "low", configured: true},
		{name: "low", config: &llm.ReasoningConfig{Level: llm.ReasoningLevelLow}, effort: "low", configured: true},
		{name: "medium", config: &llm.ReasoningConfig{Level: llm.ReasoningLevelMedium}, effort: "medium", configured: true},
		{name: "high", config: &llm.ReasoningConfig{Level: llm.ReasoningLevelHigh}, effort: "high", configured: true},
		{
			name:       "summary",
			config:     &llm.ReasoningConfig{Level: llm.ReasoningLevelLow, IncludeThoughts: boolPtr(true)},
			effort:     "low",
			summary:    "auto",
			configured: true,
		},
		{
			name:       "summary only",
			config:     &llm.ReasoningConfig{IncludeThoughts: boolPtr(true)},
			summary:    "auto",
			configured: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params, err := model.requestParams(base(test.config))
			if err != nil {
				t.Fatalf("requestParams: %v", err)
			}
			if got := string(params.Reasoning.Effort); got != test.effort {
				t.Errorf("effort = %q, want %q", got, test.effort)
			}
			if got := string(params.Reasoning.Summary); got != test.summary {
				t.Errorf("summary = %q, want %q", got, test.summary)
			}
			if test.configured {
				found := false
				for _, include := range params.Include {
					if include == responses.ResponseIncludableReasoningEncryptedContent {
						found = true
					}
				}
				if !found {
					t.Errorf("include = %v, want encrypted reasoning", params.Include)
				}
			}
		})
	}

	if _, err := model.requestParams(base(&llm.ReasoningConfig{Level: llm.ReasoningLevelNone})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("none err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base(&llm.ReasoningConfig{BudgetTokens: intPtr(1024)})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("budget err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base(&llm.ReasoningConfig{Level: "extreme"})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("level err = %v, want ErrUnsupported", err)
	}
}

func TestRequestIncludesEncryptedReasoningForReasoningParts(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	reasoning := llm.NewReasoningPart("")
	reasoning.Summary = []string{"summary"}
	if err := attachState(reasoning, partState{ItemID: "rs_1"}); err != nil {
		t.Fatalf("attach state: %v", err)
	}
	params, err := model.requestParams(&llm.Request{
		Messages: []llm.Message{
			llm.UserMessage("hi"),
			llm.AssistantMessageWithParts(reasoning),
		},
	})
	if err != nil {
		t.Fatalf("requestParams: %v", err)
	}
	found := false
	for _, include := range params.Include {
		if include == responses.ResponseIncludableReasoningEncryptedContent {
			found = true
		}
	}
	if !found {
		t.Fatalf("include = %v, want encrypted reasoning", params.Include)
	}
}

func TestConvertResponseFormat(t *testing.T) {
	text, err := convertResponseFormat(nil)
	if err != nil {
		t.Fatalf("nil format: %v", err)
	}
	if !isZeroTextConfig(text) {
		t.Fatalf("nil format = %+v, want zero", text)
	}

	jsonFormat, err := convertResponseFormat(&llm.ResponseFormat{Type: llm.ResponseFormatJSON})
	if err != nil {
		t.Fatalf("json format: %v", err)
	}
	if jsonFormat.Format.OfJSONObject == nil {
		t.Fatalf("json format = %+v", jsonFormat.Format)
	}

	schemaFormat, err := convertResponseFormat(&llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Strict: true,
		Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("schema format: %v", err)
	}
	if schemaFormat.Format.OfJSONSchema == nil || !schemaFormat.Format.OfJSONSchema.Strict.Value {
		t.Fatalf("schema format = %+v", schemaFormat.Format.OfJSONSchema)
	}
	if schemaFormat.Format.OfJSONSchema.Name != "output" {
		t.Fatalf("name = %q", schemaFormat.Format.OfJSONSchema.Name)
	}

	if _, err := convertResponseFormat(&llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("empty schema err = %v, want ErrUnsupported", err)
	}
	if _, err := convertResponseFormat(&llm.ResponseFormat{Type: "xml"}); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("xml err = %v, want ErrUnsupported", err)
	}
}

func isZeroTextConfig(config responses.ResponseTextConfigParam) bool {
	return config.Format.OfJSONObject == nil &&
		config.Format.OfJSONSchema == nil &&
		config.Format.OfText == nil
}

func TestConvertTools(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	base := func(tools []llm.ToolDefinition, choice llm.ToolChoice) *llm.Request {
		return &llm.Request{
			Messages:   []llm.Message{llm.UserMessage("hi")},
			Tools:      tools,
			ToolChoice: choice,
		}
	}

	params, err := model.requestParams(base([]llm.ToolDefinition{
		{
			Name:        "lookup",
			Description: "find a thing",
			Strict:      true,
			Parameters:  map[string]any{"type": "object"},
		},
		{
			Name:       "notes",
			Kind:       llm.ToolKindMCP,
			Parameters: map[string]any{"type": "object"},
		},
		{
			Name:     "web_search",
			Kind:     llm.ToolKindServer,
			Provider: providerData(t, map[string]any{"type": "web_search_preview"}),
		},
	}, llm.ToolChoice{Mode: llm.ToolChoiceRequired}))
	if err != nil {
		t.Fatalf("requestParams: %v", err)
	}
	if len(params.Tools) != 3 {
		t.Fatalf("tools = %+v", params.Tools)
	}
	if params.Tools[0].OfFunction == nil || params.Tools[0].OfFunction.Name != "lookup" {
		t.Fatalf("function tool = %+v", params.Tools[0])
	}
	if !params.Tools[0].OfFunction.Strict.Value {
		t.Fatalf("strict = %+v", params.Tools[0].OfFunction.Strict)
	}
	if params.Tools[1].OfFunction == nil || params.Tools[1].OfFunction.Name != "notes" {
		t.Fatalf("mcp tool should map to function: %+v", params.Tools[1])
	}
	if params.Tools[2].OfWebSearchPreview == nil {
		t.Fatalf("hosted tool = %+v", params.Tools[2])
	}
	if params.ToolChoice.OfToolChoiceMode.Value != responses.ToolChoiceOptionsRequired {
		t.Fatalf("tool choice = %+v", params.ToolChoice)
	}

	params, err = model.requestParams(base(paramsTools(params), llm.ToolChoice{Name: "lookup"}))
	if err != nil {
		t.Fatalf("name choice: %v", err)
	}
	if params.ToolChoice.OfFunctionTool == nil || params.ToolChoice.OfFunctionTool.Name != "lookup" {
		t.Fatalf("name choice = %+v", params.ToolChoice)
	}

	for _, mode := range []llm.ToolChoiceMode{llm.ToolChoiceAuto, llm.ToolChoiceNone, llm.ToolChoiceRequired} {
		params, err := model.requestParams(base(paramsTools(params), llm.ToolChoice{Mode: mode}))
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if params.ToolChoice.OfToolChoiceMode.Value != responses.ToolChoiceOptions(mode) {
			t.Errorf("%s: choice = %+v", mode, params.ToolChoice)
		}
	}

	if _, err := model.requestParams(base(nil, llm.ToolChoice{Mode: llm.ToolChoiceRequired})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("required without tools err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base([]llm.ToolDefinition{
		{Name: "web_search", Kind: llm.ToolKindServer, Provider: providerData(t, map[string]any{"type": "web_search_preview"})},
	}, llm.ToolChoice{Name: "web_search"})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("server choice err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base([]llm.ToolDefinition{
		{Name: "lookup", Parameters: map[string]any{"type": "object"}},
		{Name: "lookup", Parameters: map[string]any{"type": "object"}},
	}, llm.ToolChoice{})); !errors.Is(err, llm.ErrInvalidToolCall) {
		t.Fatalf("duplicate err = %v, want ErrInvalidToolCall", err)
	}
	if _, err := model.requestParams(base([]llm.ToolDefinition{
		{Name: "web_search", Kind: llm.ToolKindServer},
	}, llm.ToolChoice{})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("hosted without provider err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base([]llm.ToolDefinition{
		{Name: "lookup", Parameters: map[string]any{"type": "array"}},
	}, llm.ToolChoice{})); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("non-object schema err = %v, want ErrUnsupported", err)
	}
	if _, err := model.requestParams(base([]llm.ToolDefinition{
		{Name: "", Parameters: map[string]any{"type": "object"}},
	}, llm.ToolChoice{})); !errors.Is(err, llm.ErrInvalidToolCall) {
		t.Fatalf("missing name err = %v, want ErrInvalidToolCall", err)
	}
}

func paramsTools(params responses.ResponseNewParams) []llm.ToolDefinition {
	tools := make([]llm.ToolDefinition, 0, len(params.Tools))
	for _, tool := range params.Tools {
		if tool.OfFunction != nil {
			tools = append(tools, llm.ToolDefinition{
				Name:       tool.OfFunction.Name,
				Parameters: map[string]any{"type": "object"},
			})
		}
	}
	return tools
}

func TestRequestRejectsUnsupportedOptions(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	base := func() *llm.Request {
		return &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}}
	}

	request := base()
	request.Stop = []string{"END"}
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("stop err = %v, want ErrUnsupported", err)
	}

	request = base()
	request.ProviderOptions = providerData(t, map[string]any{"foo": "bar"})
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("provider options err = %v, want ErrUnsupported", err)
	}

	request = base()
	request.MaxTokens = intPtr(0)
	if _, err := model.requestParams(request); !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("zero max tokens err = %v, want ErrUnsupported", err)
	}

	request = base()
	request.Temperature = floatPtr(0.5)
	request.TopP = floatPtr(0.5)
	request.MaxTokens = intPtr(10)
	params, err := model.requestParams(request)
	if err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if params.Temperature.Value != 0.5 || params.TopP.Value != 0.5 || params.MaxOutputTokens.Value != 10 {
		t.Fatalf("sampling = %+v", params)
	}
}

func TestRequestRejectsNilAndInvalidMessages(t *testing.T) {
	model := &Model{modelID: "gpt-test"}
	params, err := model.requestParams(nil)
	if err != nil {
		t.Fatalf("nil request: %v", err)
	}
	if len(params.Input.OfInputItemList) != 0 {
		t.Fatalf("nil request input = %+v", params.Input)
	}

	if _, err := model.requestParams(&llm.Request{Messages: []llm.Message{{Role: "narrator"}}}); err == nil {
		t.Fatal("invalid role succeeded")
	}
}
