// Package llm provides interfaces, message definitions, and provider abstractions
// for interacting with Large Language Models.
package llm

import (
	"context"
)

// Role represents the role of a message participant.
type Role string

const (
	// RoleSystem is for high-level instructions directing model behavior.
	RoleSystem Role = "system"
	// RoleUser represents the end-user or human input.
	RoleUser Role = "user"
	// RoleAssistant represents responses from the model.
	RoleAssistant Role = "assistant"
	// RoleTool represents tool execution outputs returned to the model.
	RoleTool Role = "tool"
)

// Message represents a single chat message in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// FinishReason represents why the LLM stopped generating tokens.
type FinishReason string

// Finish reasons returned by an LLM generation.
const (
	FinishReasonStop   FinishReason = "stop"
	FinishReasonLength FinishReason = "length"
	FinishReasonTool   FinishReason = "tool_calls"
	FinishReasonError  FinishReason = "error"
)

// Usage contains token consumption statistics for a generation request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GenerateRequest contains the input parameters for a model generation.
type GenerateRequest struct {
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	StopWords   []string  `json:"stop_words,omitempty"`
}

// GenerateResponse represents the output from a model generation.
type GenerateResponse struct {
	Text         string       `json:"text"`
	FinishReason FinishReason `json:"finish_reason"`
	Usage        Usage        `json:"usage"`
	Raw          any          `json:"raw,omitempty"`
}

// LLM is the core swappable interface for Large Language Models.
// Any provider (OpenAI, Anthropic, Gemini, Ollama, local models, or mocks)
// can be implemented behind this interface.
type LLM interface {
	// Generate generates a non-streaming response for the given request.
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
	// ModelID returns the identifier of the model (e.g. "gpt-4o", "claude-3-5-sonnet", "mock-llm").
	ModelID() string
}

// Model is an alias for LLM.
type Model = LLM
