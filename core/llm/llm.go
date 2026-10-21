// Package llm provides interfaces, message definitions, and provider abstractions
// for interacting with Large Language Models.
//
// The package is organized around a few core concepts:
//
//   - Parts and messages describe multimodal conversation input.
//   - Requests and responses describe provider-agnostic generation.
//   - Streams provide incremental generation.
//   - LLM is the swappable provider contract.
package llm

import "context"

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
