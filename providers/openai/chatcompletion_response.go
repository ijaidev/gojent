package openai

import (
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	sdk "github.com/openai/openai-go"
)

// canonicalResponse converts one non-streaming Chat Completions response into
// canonical form.
func (m *ChatCompletionModel) canonicalResponse(completion *sdk.ChatCompletion) (*llm.Response, error) {
	if completion == nil {
		return nil, fmt.Errorf("%w: nil response", llm.ErrInvalidMessage)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("%w: response contains no choices", llm.ErrInvalidMessage)
	}

	choice := completion.Choices[0]
	parts, err := canonicalChatParts(&choice.Message)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("openai: response contains no content: %w", llm.ErrInvalidMessage)
	}

	finish, status := mapChatFinishReason(choice.FinishReason)
	modelID := completion.Model
	if modelID == "" {
		modelID = m.modelID
	}
	message := llm.Message{
		Role:    llm.RoleAssistant,
		Content: parts,
		Status:  status,
	}
	if err := message.Validate(); err != nil {
		return nil, err
	}

	return &llm.Response{
		ID:              completion.ID,
		Model:           modelID,
		Message:         message,
		Status:          status,
		FinishReason:    finish,
		RawFinishReason: choice.FinishReason,
		Usage:           chatUsage(completion.Usage),
	}, nil
}

func canonicalChatParts(message *sdk.ChatCompletionMessage) ([]llm.Part, error) {
	if message == nil {
		return nil, fmt.Errorf("%w: nil message", llm.ErrInvalidMessage)
	}

	parts := make([]llm.Part, 0, 1+len(message.ToolCalls))
	if message.Content != "" {
		parts = append(parts, llm.NewTextPart(message.Content))
	}
	if message.Refusal != "" {
		parts = append(parts, llm.NewRefusalPart(message.Refusal))
	}
	for index := range message.ToolCalls {
		call := &message.ToolCalls[index]
		if call.ID == "" {
			return nil, fmt.Errorf("%w: tool call %d has no id", llm.ErrInvalidToolCall, index)
		}
		if call.Function.Name == "" {
			return nil, fmt.Errorf("%w: tool call %d has no name", llm.ErrInvalidToolCall, index)
		}
		input, err := canonicalArguments(call.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tool call %d: %w", index, err)
		}
		parts = append(parts, llm.NewToolCallPart(llm.ToolCall{
			CallID: call.ID,
			Name:   call.Function.Name,
			Input:  input,
		}))
	}
	return parts, nil
}

func mapChatFinishReason(reason string) (llm.FinishReason, llm.MessageStatus) {
	switch reason {
	case "", "stop":
		return llm.FinishReasonStop, llm.MessageStatusCompleted
	case "length":
		return llm.FinishReasonLength, llm.MessageStatusIncomplete
	case "tool_calls", "function_call":
		return llm.FinishReasonTool, llm.MessageStatusCompleted
	case "content_filter":
		return llm.FinishReasonContentFilter, llm.MessageStatusIncomplete
	default:
		return llm.FinishReason(strings.ToLower(reason)), llm.MessageStatusIncomplete
	}
}

func chatUsage(usage sdk.CompletionUsage) llm.Usage {
	return llm.Usage{
		PromptTokens:     int(usage.PromptTokens),
		CompletionTokens: int(usage.CompletionTokens),
		TotalTokens:      int(usage.TotalTokens),
		ReasoningTokens:  int(usage.CompletionTokensDetails.ReasoningTokens),
		CachedTokens:     int(usage.PromptTokensDetails.CachedTokens),
	}
}
