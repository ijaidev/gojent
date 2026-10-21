package llm

// FinishReason represents why the LLM stopped generating tokens.
type FinishReason string

// Finish reasons returned by an LLM generation.
const (
	FinishReasonStop   FinishReason = "stop"
	FinishReasonLength FinishReason = "length"
	FinishReasonTool   FinishReason = "tool_calls"
)

// Usage contains token consumption statistics for a generation request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// GenerateResponse represents the output from a model generation.
type GenerateResponse struct {
	Text         string       `json:"text"`
	Thinking     string       `json:"thinking,omitempty"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
	FinishReason FinishReason `json:"finish_reason"`
	Usage        Usage        `json:"usage"`
	Raw          any          `json:"raw,omitempty"`
}
