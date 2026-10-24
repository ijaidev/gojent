package llm

// FinishReason explains why generation ended.
type FinishReason string

const (
	// FinishReasonStop means the model finished normally.
	FinishReasonStop FinishReason = "stop"
	// FinishReasonLength means the model hit a token limit.
	FinishReasonLength FinishReason = "length"
	// FinishReasonTool means the model requested one or more tools.
	FinishReasonTool FinishReason = "tool_calls"
	// FinishReasonContentFilter means the provider blocked the output.
	FinishReasonContentFilter FinishReason = "content_filter"
)

// Usage contains token consumption statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

// Add returns the sum of two usages.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		PromptTokens:     u.PromptTokens + other.PromptTokens,
		CompletionTokens: u.CompletionTokens + other.CompletionTokens,
		TotalTokens:      u.TotalTokens + other.TotalTokens,
		ReasoningTokens:  u.ReasoningTokens + other.ReasoningTokens,
		CachedTokens:     u.CachedTokens + other.CachedTokens,
	}
}

// Response is one complete model response.
type Response struct {
	// Message is the canonical assistant message. Its Content, ToolCalls, and
	// ProviderData are authoritative.
	Message Message `json:"message"`
	// FinishReason explains why generation ended.
	FinishReason FinishReason `json:"finish_reason"`
	// Usage reports token consumption for this call.
	Usage Usage `json:"usage"`
}
