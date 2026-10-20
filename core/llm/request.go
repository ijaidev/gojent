package llm

// ThinkingLevel represents the normalized reasoning effort levels across providers.
type ThinkingLevel string

const (
	// ThinkingLevelUnspecified indicates no preference; provider default is used.
	ThinkingLevelUnspecified ThinkingLevel = ""
	// ThinkingLevelNone explicitly disables internal model reasoning/thinking.
	ThinkingLevelNone ThinkingLevel = "none"
	// ThinkingLevelMinimal requests minimal reasoning steps for lower latency and cost.
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	// ThinkingLevelLow requests low reasoning depth.
	ThinkingLevelLow ThinkingLevel = "low"
	// ThinkingLevelMedium requests balanced reasoning depth (typical default on reasoning models).
	ThinkingLevelMedium ThinkingLevel = "medium"
	// ThinkingLevelHigh requests maximum reasoning depth for complex math, coding, and multi-step tasks.
	ThinkingLevelHigh ThinkingLevel = "high"
)

// ThinkingConfig defines parameters for model reasoning/thinking.
type ThinkingConfig struct {
	// Level specifies the qualitative reasoning effort (e.g. "minimal", "low", "medium", "high").
	Level ThinkingLevel `json:"level,omitempty"`
	// BudgetTokens optionally specifies an explicit token budget limit (0 to disable, or a positive token count).
	BudgetTokens *int `json:"budget_tokens,omitempty"`
	// IncludeThoughts indicates whether thoughts should be returned in responses if supported.
	IncludeThoughts *bool `json:"include_thoughts,omitempty"`
}

// Clone returns a deep copy of ThinkingConfig.
func (c *ThinkingConfig) Clone() *ThinkingConfig {
	if c == nil {
		return nil
	}
	cp := *c
	if c.BudgetTokens != nil {
		b := *c.BudgetTokens
		cp.BudgetTokens = &b
	}
	if c.IncludeThoughts != nil {
		it := *c.IncludeThoughts
		cp.IncludeThoughts = &it
	}
	return &cp
}

// ResponseFormatType defines the expected format of the model output.
type ResponseFormatType string

const (
	// ResponseFormatText produces standard unconstrained natural language text.
	ResponseFormatText ResponseFormatType = "text"
	// ResponseFormatJSON requests raw valid JSON output without a constrained schema.
	ResponseFormatJSON ResponseFormatType = "json"
	// ResponseFormatJSONSchema requests structured output strictly conforming to a given JSON Schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat defines constraints for structured model output.
type ResponseFormat struct {
	// Type specifies whether output should be text, arbitrary JSON, or JSON conforming to a schema.
	Type ResponseFormatType `json:"type"`
	// Name optionally specifies the schema identifier (required by some providers like OpenAI).
	Name string `json:"name,omitempty"`
	// Description optionally explains the purpose of the output schema.
	Description string `json:"description,omitempty"`
	// Schema is the JSON Schema definition (typically a map[string]any or a struct).
	Schema any `json:"schema,omitempty"`
	// Strict indicates whether the provider should enforce strict schema adherence.
	Strict *bool `json:"strict,omitempty"`
}

// Clone returns a deep copy of ResponseFormat.
func (rf *ResponseFormat) Clone() *ResponseFormat {
	if rf == nil {
		return nil
	}
	cp := *rf
	if rf.Strict != nil {
		s := *rf.Strict
		cp.Strict = &s
	}
	return &cp
}

// GenerateRequest contains the input parameters for a model generation.
type GenerateRequest struct {
	Messages       []Message        `json:"messages"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	Thinking       *ThinkingConfig  `json:"thinking,omitempty"`
	ResponseFormat *ResponseFormat  `json:"response_format,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	MaxTokens      *int             `json:"max_tokens,omitempty"`
	TopP           *float64         `json:"top_p,omitempty"`
	StopWords      []string         `json:"stop_words,omitempty"`
}
