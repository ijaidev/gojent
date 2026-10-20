package llm_test

import (
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
		Type:        llm.ResponseFormatJSONSchema,
		Name:        "TestSchema",
		Description: "A test schema",
		Schema:      map[string]any{"type": "object"},
		Strict:      &strict,
	}

	cloned := rf.Clone()
	if cloned.Type != llm.ResponseFormatJSONSchema || cloned.Name != "TestSchema" || *cloned.Strict != true {
		t.Fatalf("unexpected cloned format: %+v", cloned)
	}

	*rf.Strict = false
	if *cloned.Strict != true {
		t.Errorf("expected clone strict to remain true, got %v", *cloned.Strict)
	}
}
