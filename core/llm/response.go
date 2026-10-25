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

// Usage contains token consumption statistics. Usage is the terminal snapshot
// a provider reports; it is never summed across streaming chunks.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

// Response is one complete model response.
type Response struct {
	// ID is the provider response identifier.
	ID string `json:"id,omitempty"`
	// Model is the provider model identifier.
	Model string `json:"model,omitempty"`
	// Message is the canonical assistant message.
	Message Message `json:"message"`
	// Status reports provider message status.
	Status MessageStatus `json:"status,omitempty"`
	// FinishReason is the normalized terminal reason.
	FinishReason FinishReason `json:"finish_reason"`
	// RawFinishReason is the provider's original reason string.
	RawFinishReason string `json:"raw_finish_reason,omitempty"`
	// Usage is the terminal token snapshot.
	Usage Usage `json:"usage"`
	// Provider carries message-level provider metadata.
	Provider ProviderData `json:"provider,omitempty"`
}
