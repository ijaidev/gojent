// Package llm provides interfaces, message definitions, and provider abstractions
// for interacting with Large Language Models.
package llm

import (
	"context"
	"encoding/json"
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

// ToolCall represents a model-requested invocation of an external tool or function.
type ToolCall struct {
	// ID is the unique identifier for this tool call event.
	ID string `json:"id"`
	// Name is the name of the tool requested by the model.
	Name string `json:"name"`
	// Arguments is the JSON-encoded string of arguments provided by the model.
	Arguments string `json:"arguments"`
}

// UnmarshalArgs unmarshals the tool call's JSON arguments into the target destination.
func (tc ToolCall) UnmarshalArgs(target any) error {
	return json.Unmarshal([]byte(tc.Arguments), target)
}

// ToolDefinition describes a tool schema exposed to the model for function calling.
type ToolDefinition struct {
	// Name is the unique name of the tool.
	Name string `json:"name"`
	// Description explains what the tool does and when it should be invoked.
	Description string `json:"description"`
	// Parameters specifies the JSON Schema defining the expected inputs.
	Parameters any `json:"parameters,omitempty"`
}

// Message represents a single chat message in a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Raw holds provider-specific message/content payload (e.g. Gemini thought signatures or OpenAI metadata).
	Raw any `json:"raw,omitempty"`
}

// UserMessage creates a user message with the given content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// SystemMessage creates a system instruction message with the given content.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

// AssistantMessage creates an assistant message with the given content.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

// ToolResultMessage creates a tool execution output message for a specific tool call ID.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    content,
	}
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
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	StopWords   []string         `json:"stop_words,omitempty"`
}

// GenerateResponse represents the output from a model generation.
type GenerateResponse struct {
	Text         string       `json:"text"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
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
	// Stream generates a streaming response for the given request.
	Stream(ctx context.Context, req GenerateRequest) (Stream, error)
	// ModelID returns the identifier of the model (e.g. "gpt-4o", "claude-3-5-sonnet", "mock-llm").
	ModelID() string
}

// Model is an alias for LLM.
type Model = LLM
