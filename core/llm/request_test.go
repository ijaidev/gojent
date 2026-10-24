package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestThinkingConfig_Clone(t *testing.T) {
	budget := 4096
	includeThoughts := true
	cfg := llm.ThinkingConfig{
		Level:           llm.ThinkingLevelMedium,
		BudgetTokens:    &budget,
		IncludeThoughts: &includeThoughts,
	}

	cloned := cfg.Clone()
	if cloned.Level != llm.ThinkingLevelMedium || *cloned.BudgetTokens != 4096 || *cloned.IncludeThoughts != true {
		t.Fatalf("unexpected cloned config: %+v", cloned)
	}

	// Mutating original should not affect clone
	*cfg.BudgetTokens = 2048
	if *cloned.BudgetTokens != 4096 {
		t.Errorf("expected clone to be independent, got budget %d", *cloned.BudgetTokens)
	}
}

func TestResponseFormat_Clone(t *testing.T) {
	strict := true
	rf := &llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Name:   "TestSchema",
		Schema: map[string]any{"type": "object"},
		Strict: strict,
	}

	cloned := rf.Clone()
	if cloned.Type != llm.ResponseFormatJSONSchema || cloned.Name != "TestSchema" || !cloned.Strict {
		t.Fatalf("unexpected cloned format: %+v", cloned)
	}

	rf.Strict = false
	if !cloned.Strict {
		t.Errorf("expected clone strict to remain true, got %v", cloned.Strict)
	}
}

func TestRequest_CloneIsDeep(t *testing.T) {
	budget := 1024
	request := &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hello")},
		Tools: []llm.ToolDefinition{{
			Name: "echo",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"value": map[string]any{"type": "string"}},
			},
		}},
		Thinking:  &llm.ThinkingConfig{BudgetTokens: &budget},
		StopWords: []string{"stop"},
		Metadata:  map[string]string{"tenant": "a"},
	}

	clone := request.Clone()
	clone.Messages[0].Content[0] = llm.TextPart("changed")
	clone.Tools[0].Parameters["type"] = "string"
	clone.Tools[0].Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"] = "number"
	*clone.Thinking.BudgetTokens = 2048
	clone.StopWords[0] = "changed"
	clone.Metadata["tenant"] = "b"

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
	if *request.Thinking.BudgetTokens != 1024 {
		t.Errorf("thinking budget = %d", *request.Thinking.BudgetTokens)
	}
	if request.StopWords[0] != "stop" {
		t.Errorf("stop words = %v", request.StopWords)
	}
	if request.Metadata["tenant"] != "a" {
		t.Errorf("metadata = %v", request.Metadata)
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
		t.Fatalf("Unmarshal: %v", err)
	}
	choice, ok := raw["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %v, want object", raw["tool_choice"])
	}
	if choice["mode"] != "required" || choice["name"] != "lookup" {
		t.Errorf("tool_choice = %v", choice)
	}

	empty, err := json.Marshal(llm.Request{})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if len(empty) == 0 {
		t.Fatal("empty request encoded as nothing")
	}
	var emptyRaw map[string]any
	if err := json.Unmarshal(empty, &emptyRaw); err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if _, present := emptyRaw["tool_choice"]; present {
		t.Errorf("zero ToolChoice should be omitted, got %s", empty)
	}
}
