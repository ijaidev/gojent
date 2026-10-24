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

// ResponseFormatType defines the expected output format.
type ResponseFormatType string

const (
	// ResponseFormatText produces unconstrained text.
	ResponseFormatText ResponseFormatType = "text"
	// ResponseFormatJSON produces valid JSON.
	ResponseFormatJSON ResponseFormatType = "json"
	// ResponseFormatJSONSchema produces JSON conforming to Schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat constrains structured output.
type ResponseFormat struct {
	// Type selects text, JSON, or JSON Schema output.
	Type ResponseFormatType `json:"type"`
	// Name identifies the schema for providers that require a name.
	Name string `json:"name,omitempty"`
	// Schema is a JSON Schema object.
	Schema map[string]any `json:"schema,omitempty"`
	// Strict asks the provider to enforce the schema exactly.
	Strict bool `json:"strict,omitempty"`
}

// Clone returns a deep copy.
func (rf *ResponseFormat) Clone() *ResponseFormat {
	if rf == nil {
		return nil
	}
	cp := *rf
	if rf.Schema != nil {
		cp.Schema = cloneSchema(rf.Schema)
	}
	return &cp
}

func cloneSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch value := v.(type) {
		case map[string]any:
			out[k] = cloneSchema(value)
		case []any:
			items := make([]any, len(value))
			for i, item := range value {
				if nested, ok := item.(map[string]any); ok {
					items[i] = cloneSchema(nested)
				} else {
					items[i] = item
				}
			}
			out[k] = items
		case []string:
			out[k] = append([]string(nil), value...)
		default:
			out[k] = v
		}
	}
	return out
}

// Request contains the provider-neutral input for one model call.
type Request struct {
	Messages       []Message        `json:"messages"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	ToolChoice     ToolChoice       `json:"tool_choice,omitempty"`
	Thinking       *ThinkingConfig  `json:"thinking,omitempty"`
	ResponseFormat *ResponseFormat  `json:"response_format,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	MaxTokens      *int             `json:"max_tokens,omitempty"`
	TopP           *float64         `json:"top_p,omitempty"`
	StopWords      []string         `json:"stop_words,omitempty"`
	// Metadata is passed to the provider when supported (for example OpenAI's
	// user field). It is never sent to the model as content.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Clone returns a deep copy of the request.
func (r *Request) Clone() *Request {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Messages = make([]Message, len(r.Messages))
	for i, msg := range r.Messages {
		clone.Messages[i] = msg.Clone()
	}
	clone.Tools = append([]ToolDefinition(nil), r.Tools...)
	clone.Thinking = r.Thinking.Clone()
	clone.ResponseFormat = r.ResponseFormat.Clone()
	if r.MaxTokens != nil {
		v := *r.MaxTokens
		clone.MaxTokens = &v
	}
	if r.Temperature != nil {
		v := *r.Temperature
		clone.Temperature = &v
	}
	if r.TopP != nil {
		v := *r.TopP
		clone.TopP = &v
	}
	clone.StopWords = append([]string(nil), r.StopWords...)
	if r.Metadata != nil {
		clone.Metadata = make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}
