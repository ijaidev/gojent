package agent

import (
	"context"
	"fmt"
)

// GenerateStructured runs the agent with a string prompt and structured output
// conforming to Go type T. It configures the response schema from T, unmarshals
// the model output, and returns both the decoded value and the agent response.
//
// For multimodal input, pass WithMedia as an option.
func GenerateStructured[T any](ctx context.Context, a *Agent, prompt string, opts ...GenerateOption) (T, *Response, error) {
	allOpts := append([]GenerateOption{WithOutputSchema[T]()}, opts...)
	res, err := a.Generate(ctx, prompt, allOpts...)
	return decodeStructured[T](res, err)
}

// decodeStructured converts a raw agent response into T while preserving the
// originating response for callers that need usage, messages, or metadata.
func decodeStructured[T any](res *Response, err error) (T, *Response, error) {
	var zero T
	if err != nil {
		return zero, nil, err
	}

	var data T
	if err := res.Unmarshal(&data); err != nil {
		return zero, res, fmt.Errorf("agent: failed to unmarshal structured response into %T: %w", zero, err)
	}
	return data, res, nil
}
