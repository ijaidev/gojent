package llm_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestReasoningConfig_Clone(t *testing.T) {
	budget := 4096
	include := true
	config := llm.ReasoningConfig{
		Level:           llm.ReasoningLevelMedium,
		BudgetTokens:    &budget,
		IncludeThoughts: &include,
	}
	clone := config.Clone()
	if clone.Level != llm.ReasoningLevelMedium || *clone.BudgetTokens != 4096 || !*clone.IncludeThoughts {
		t.Fatalf("clone = %+v", clone)
	}
	*config.BudgetTokens = 2048
	*config.IncludeThoughts = false
	if *clone.BudgetTokens != 4096 || !*clone.IncludeThoughts {
		t.Errorf("clone aliases original: %+v", clone)
	}
}

func TestResponseFormat_Clone(t *testing.T) {
	format := &llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Name:   "TestSchema",
		Schema: map[string]any{"type": "object"},
		Strict: true,
	}
	clone := format.Clone()
	if clone.Type != format.Type || clone.Name != format.Name || !clone.Strict {
		t.Fatalf("clone = %+v", clone)
	}
	format.Strict = false
	format.Schema["type"] = "string"
	if !clone.Strict || clone.Schema["type"] != "object" {
		t.Errorf("clone aliases original: %+v", clone)
	}
}

func TestRequest_CloneIsDeep(t *testing.T) {
	budget := 1024
	store := true
	request := &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hello")},
		Tools: []llm.ToolDefinition{{
			Name: "echo",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"value": map[string]any{"type": "string"}},
			},
			Provider: llm.ProviderData{"google": json.RawMessage(`{"mode":"a"}`)},
		}},
		State: llm.State{
			Mode:       llm.StateModeServer,
			PreviousID: "resp-1",
			Store:      &store,
			Provider:   llm.ProviderData{"openai": json.RawMessage(`{"x":1}`)},
		},
		Reasoning:       &llm.ReasoningConfig{BudgetTokens: &budget},
		Stop:            []string{"stop"},
		ProviderOptions: llm.ProviderData{"google": json.RawMessage(`{"y":2}`)},
	}

	clone := request.Clone()
	clone.Messages[0].Content[0].(*llm.TextPart).Text = "changed"
	clone.Tools[0].Parameters["type"] = "string"
	clone.Tools[0].Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"] = "number"
	clone.Tools[0].Provider["google"] = json.RawMessage(`{"mode":"b"}`)
	*clone.State.Store = false
	clone.State.Provider["openai"] = json.RawMessage(`{"x":2}`)
	*clone.Reasoning.BudgetTokens = 2048
	clone.Stop[0] = "changed"
	clone.ProviderOptions["google"] = json.RawMessage(`{"y":3}`)

	if got := request.Messages[0].Text(); got != "hello" {
		t.Errorf("message text = %q", got)
	}
	if got := request.Tools[0].Parameters["type"]; got != "object" {
		t.Errorf("tool schema type = %v", got)
	}
	property := request.Tools[0].Parameters["properties"].(map[string]any)["value"].(map[string]any)
	if got := property["type"]; got != "string" {
		t.Errorf("nested tool schema type = %v", got)
	}
	if string(request.Tools[0].Provider["google"]) != `{"mode":"a"}` {
		t.Errorf("tool provider = %s", request.Tools[0].Provider["google"])
	}
	if !*request.State.Store || string(request.State.Provider["openai"]) != `{"x":1}` {
		t.Errorf("state = %+v", request.State)
	}
	if *request.Reasoning.BudgetTokens != 1024 {
		t.Errorf("budget = %d", *request.Reasoning.BudgetTokens)
	}
	if request.Stop[0] != "stop" {
		t.Errorf("stop = %v", request.Stop)
	}
	if string(request.ProviderOptions["google"]) != `{"y":2}` {
		t.Errorf("provider options = %s", request.ProviderOptions["google"])
	}
}

func TestState_DefaultsToReplay(t *testing.T) {
	var state llm.State
	if state.EffectiveMode() != llm.StateModeReplay {
		t.Errorf("mode = %q", state.EffectiveMode())
	}
	if err := state.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	invalid := llm.State{Mode: "cached"}
	if err := invalid.Validate(); !errors.Is(err, llm.ErrUnsupported) {
		t.Errorf("Validate = %v, want ErrUnsupported", err)
	}
}

func TestRequest_ValidateToolPairing(t *testing.T) {
	request := &llm.Request{Messages: []llm.Message{
		llm.UserMessage("hi"),
		llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{CallID: "a", Name: "x"})),
	}}
	if err := request.Validate(); err != nil {
		t.Fatalf("trailing call should be valid: %v", err)
	}
	request.Messages = append(request.Messages, llm.UserMessage("no result"))
	if err := request.Validate(); !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("Validate = %v, want ErrInvalidMessage", err)
	}
}

func TestRequest_ToolChoiceJSON(t *testing.T) {
	encoded, err := json.Marshal(llm.Request{
		ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceRequired, Name: "lookup"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	choice, ok := raw["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %v", raw["tool_choice"])
	}
	if choice["mode"] != "required" || choice["name"] != "lookup" {
		t.Errorf("tool_choice = %v", choice)
	}

	empty, err := json.Marshal(llm.Request{})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	var emptyRaw map[string]any
	if err := json.Unmarshal(empty, &emptyRaw); err != nil {
		t.Fatal(err)
	}
	if _, present := emptyRaw["tool_choice"]; present {
		t.Errorf("zero ToolChoice should be omitted, got %s", empty)
	}
	if _, present := emptyRaw["state"]; present {
		t.Errorf("zero State should be omitted, got %s", empty)
	}
}
