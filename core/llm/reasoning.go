package llm

// ReasoningLevel represents normalized reasoning effort across providers.
type ReasoningLevel string

const (
	// ReasoningLevelNone disables reasoning.
	ReasoningLevelNone ReasoningLevel = "none"
	// ReasoningLevelMinimal requests minimal reasoning.
	ReasoningLevelMinimal ReasoningLevel = "minimal"
	// ReasoningLevelLow requests low reasoning depth.
	ReasoningLevelLow ReasoningLevel = "low"
	// ReasoningLevelMedium requests balanced reasoning depth.
	ReasoningLevelMedium ReasoningLevel = "medium"
	// ReasoningLevelHigh requests maximum reasoning depth.
	ReasoningLevelHigh ReasoningLevel = "high"
)

// ReasoningConfig configures model reasoning.
type ReasoningConfig struct {
	// Level is the qualitative reasoning effort.
	Level ReasoningLevel `json:"level,omitempty"`
	// BudgetTokens is an explicit token budget, when supported.
	BudgetTokens *int `json:"budget_tokens,omitempty"`
	// IncludeThoughts requests that reasoning be returned.
	IncludeThoughts *bool `json:"include_thoughts,omitempty"`
}

// Clone returns a deep copy.
func (c *ReasoningConfig) Clone() *ReasoningConfig {
	if c == nil {
		return nil
	}
	clone := *c
	clone.BudgetTokens = clonePtr(c.BudgetTokens)
	clone.IncludeThoughts = clonePtr(c.IncludeThoughts)
	return &clone
}
