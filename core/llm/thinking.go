package llm

// ThinkingLevel represents normalized reasoning effort across providers.
type ThinkingLevel string

const (
	// ThinkingLevelNone disables reasoning.
	ThinkingLevelNone ThinkingLevel = "none"
	// ThinkingLevelMinimal requests minimal reasoning.
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	// ThinkingLevelLow requests low reasoning depth.
	ThinkingLevelLow ThinkingLevel = "low"
	// ThinkingLevelMedium requests balanced reasoning depth.
	ThinkingLevelMedium ThinkingLevel = "medium"
	// ThinkingLevelHigh requests maximum reasoning depth.
	ThinkingLevelHigh ThinkingLevel = "high"
)

// ThinkingConfig configures model reasoning.
type ThinkingConfig struct {
	// Level is the qualitative reasoning effort.
	Level ThinkingLevel `json:"level,omitempty"`
	// BudgetTokens is an explicit token budget, when supported.
	BudgetTokens *int `json:"budget_tokens,omitempty"`
	// IncludeThoughts requests that reasoning be returned.
	IncludeThoughts *bool `json:"include_thoughts,omitempty"`
}

// Clone returns a deep copy.
func (c *ThinkingConfig) Clone() *ThinkingConfig {
	if c == nil {
		return nil
	}
	cp := *c
	if c.BudgetTokens != nil {
		v := *c.BudgetTokens
		cp.BudgetTokens = &v
	}
	if c.IncludeThoughts != nil {
		v := *c.IncludeThoughts
		cp.IncludeThoughts = &v
	}
	return &cp
}
