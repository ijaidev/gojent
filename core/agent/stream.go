package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Stream runs the agent with a string prompt and returns an active StreamResponse.
func (a *Agent) Stream(ctx context.Context, prompt string, opts ...GenerateOption) (*StreamResponse, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.StreamMessages(ctx, userMsg, opts...)
}

// StreamMessages executes the agent with a slice of chat messages and returns an active StreamResponse.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...GenerateOption) (*StreamResponse, error) {
	r, err := a.newRun(ctx, messages, opts)
	if err != nil {
		return nil, err
	}

	req := r.request(r.messages)
	if err := r.hooks.beforeModel(ctx, 0, &req); err != nil {
		return nil, err
	}

	rawStream, err := r.model.Stream(ctx, req)
	if err != nil {
		r.hooks.afterModel(ctx, 0, &req, nil, err)
		return nil, err
	}

	return newStreamResponse(ctx, r, req, rawStream), nil
}
