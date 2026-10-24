package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Generate runs the agent with a text prompt.
func (a *Agent) Generate(ctx context.Context, prompt string, opts ...Option) (*Response, error) {
	return a.GenerateMessages(ctx, promptMessages(prompt), opts...)
}

// GenerateWith runs the agent with a complete RunOptions value.
func (a *Agent) GenerateWith(ctx context.Context, prompt string, opts RunOptions) (*Response, error) {
	return a.GenerateMessagesWith(ctx, promptMessages(prompt), opts)
}

// GenerateMessages runs the agent with explicit messages.
func (a *Agent) GenerateMessages(ctx context.Context, messages []llm.Message, opts ...Option) (*Response, error) {
	return a.GenerateMessagesWith(ctx, messages, resolveOptions(opts))
}

// GenerateMessagesWith runs the agent with explicit messages and RunOptions.
func (a *Agent) GenerateMessagesWith(ctx context.Context, messages []llm.Message, opts RunOptions) (*Response, error) {
	r, err := newRunner(a, opts)
	if err != nil {
		return nil, err
	}
	return r.run(ctx, messages...)
}

// GenerateStructured runs the agent and decodes its output into T.
func GenerateStructured[T any](ctx context.Context, a *Agent, prompt string, opts ...Option) (T, *Response, error) {
	all := append([]Option{WithOutputSchema[T]()}, opts...)
	response, err := a.Generate(ctx, prompt, all...)
	return decodeStructured[T](response, err)
}

func decodeStructured[T any](response *Response, err error) (T, *Response, error) {
	var zero T
	if err != nil {
		return zero, nil, err
	}
	var data T
	if err := response.Unmarshal(&data); err != nil {
		return zero, response, err
	}
	return data, response, nil
}
