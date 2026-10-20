package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Generate runs the agent with a string prompt, returning the model's response.
func (a *Agent) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.GenerateMessages(ctx, userMsg, opts...)
}

// GenerateMessages executes the agent with a slice of chat messages running
// the ReAct execution loop.
func (a *Agent) GenerateMessages(ctx context.Context, messages []llm.Message, opts ...GenerateOption) (*Response, error) {
	r, err := a.newRun(ctx, messages, opts)
	if err != nil {
		return nil, err
	}

	var cumulativeUsage llm.Usage
	var lastRaw any
	callMessages := r.messages

	for step := 0; step < r.maxSteps; step++ {
		req := r.request(callMessages)

		if err := r.hooks.beforeModel(ctx, step, &req); err != nil {
			return nil, err
		}

		resp, err := r.model.Generate(ctx, req)
		r.hooks.afterModel(ctx, step, &req, resp, err)
		if err != nil {
			return nil, err
		}

		cumulativeUsage.PromptTokens += resp.Usage.PromptTokens
		cumulativeUsage.CompletionTokens += resp.Usage.CompletionTokens
		cumulativeUsage.TotalTokens += resp.Usage.TotalTokens
		cumulativeUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		lastRaw = resp.Raw

		// Append the assistant's response to the conversation and persist it.
		assistantMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   []llm.Part{llm.TextPart(resp.Text)},
			Thinking:  resp.Thinking,
			ToolCalls: resp.ToolCalls,
			Raw:       resp.Raw,
		}
		callMessages = append(callMessages, assistantMsg)
		r.saveMessages(ctx, assistantMsg)

		// Without tool calls the model provided its final response.
		if len(resp.ToolCalls) == 0 {
			return &Response{
				Text:         resp.Text,
				Thinking:     resp.Thinking,
				Messages:     callMessages,
				FinishReason: resp.FinishReason,
				Usage:        cumulativeUsage,
				Raw:          lastRaw,
			}, nil
		}

		// Execute the requested tool calls and feed their output back.
		for _, call := range resp.ToolCalls {
			_, toolParts, _ := a.executeToolCall(ctx, step, call, r.tools, r.hooks)
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolParts,
			}
			callMessages = append(callMessages, toolMsg)
			r.saveMessages(ctx, toolMsg)
		}
	}

	// The ReAct budget is exhausted. Tool calls from the final step were still
	// executed, so the transcript never ends with an unanswered tool_calls
	// message, which strict providers reject on the next turn.
	lastMsg := callMessages[len(callMessages)-1]
	return &Response{
		Text:         lastMsg.Text(),
		Thinking:     lastMsg.Thinking,
		ToolCalls:    lastMsg.ToolCalls,
		Messages:     callMessages,
		FinishReason: llm.FinishReasonLength,
		Usage:        cumulativeUsage,
		Raw:          lastRaw,
	}, nil
}
