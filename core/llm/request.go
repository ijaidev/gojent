package llm

// Request contains the provider-neutral input for one model call.
type Request struct {
	Messages       []Message        `json:"messages"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	ToolChoice     ToolChoice       `json:"tool_choice,omitzero"`
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
	clone.Messages = CloneMessages(r.Messages)
	clone.Tools = make([]ToolDefinition, len(r.Tools))
	for i, definition := range r.Tools {
		clone.Tools[i] = definition
		clone.Tools[i].Parameters = cloneSchema(definition.Parameters)
	}
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
