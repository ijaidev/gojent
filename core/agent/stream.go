package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Stream runs the agent asynchronously and returns its Run handle.
func (a *Agent) Stream(ctx context.Context, prompt string, opts ...Option) (*Run, error) {
	return a.StreamMessages(ctx, promptMessages(prompt), opts...)
}

// StreamWith runs the agent asynchronously with a complete RunOptions value.
func (a *Agent) StreamWith(ctx context.Context, prompt string, opts RunOptions) (*Run, error) {
	return a.StreamMessagesWith(ctx, promptMessages(prompt), opts)
}

// StreamMessages runs the agent asynchronously with explicit messages.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...Option) (*Run, error) {
	return a.StreamMessagesWith(ctx, messages, resolveOptions(opts))
}

// StreamMessagesWith runs the agent asynchronously with explicit messages and
// RunOptions.
func (a *Agent) StreamMessagesWith(ctx context.Context, messages []llm.Message, opts RunOptions) (*Run, error) {
	r, err := newRunner(a, opts)
	if err != nil {
		return nil, err
	}
	return r.start(ctx, messages...)
}

func promptMessages(prompt string) []llm.Message {
	if prompt == "" {
		return nil
	}
	return []llm.Message{llm.UserMessage(prompt)}
}
