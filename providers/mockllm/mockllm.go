// Package mockllm provides a deterministic, in-memory LLM implementation for
// tests, examples, and local simulation without external network calls.
package mockllm

import (
	"context"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

// Model is a thread-safe mock implementation of the llm.LLM interface.
// It is designed for unit testing, integration tests, and local simulation
// without making external network calls.
type Model struct {
	mu             sync.RWMutex
	modelID        string
	responses      []string
	callIndex      int
	calls          []llm.GenerateRequest
	customFn       func(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error)
	customStreamFn func(ctx context.Context, req llm.GenerateRequest) (llm.Stream, error)
	usage          llm.Usage
	toolCalls      []llm.ToolCall
}

var _ llm.LLM = (*Model)(nil)

// Option configures a Model.
type Option func(*Model)

// WithModelID configures a custom model identifier on Model.
func WithModelID(id string) Option {
	return func(m *Model) {
		m.modelID = id
	}
}

// WithCustomHandler configures a dynamic response generator function.
func WithCustomHandler(fn func(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error)) Option {
	return func(m *Model) {
		m.customFn = fn
	}
}

// WithCustomStreamHandler configures a dynamic stream generator function.
func WithCustomStreamHandler(fn func(ctx context.Context, req llm.GenerateRequest) (llm.Stream, error)) Option {
	return func(m *Model) {
		m.customStreamFn = fn
	}
}

// WithUsage configures custom token usage statistics returned on each response.
func WithUsage(u llm.Usage) Option {
	return func(m *Model) {
		m.usage = u
	}
}

// WithToolCalls configures tool calls returned on generation.
func WithToolCalls(calls ...llm.ToolCall) Option {
	return func(m *Model) {
		m.toolCalls = calls
	}
}

// New creates a new Model that returns the specified text responses.
//
// Usage:
//
//	// Returns "Hello, world!" for all Generate calls:
//	mock := mockllm.New("Hello, world!")
//
//	// Cycles sequentially through responses:
//	mock := mockllm.New("Answer 1", "Answer 2")
func New(texts ...string) *Model {
	responses := texts
	if len(responses) == 0 {
		responses = []string{"Mock LLM response"}
	}
	return &Model{
		modelID:   "mock-llm",
		responses: responses,
		calls:     make([]llm.GenerateRequest, 0),
		usage: llm.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

// ApplyOptions applies additional configuration options to the mock.
func (m *Model) ApplyOptions(opts ...Option) *Model {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ModelID returns the identifier of this mock model.
func (m *Model) ModelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelID
}

// Generate implements the llm.LLM interface.
func (m *Model) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	// Respect context cancellation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the request for testing inspection
	m.calls = append(m.calls, req)

	// If a custom handler is set, delegate to it
	if m.customFn != nil {
		return m.customFn(ctx, req)
	}

	text := ""
	if len(m.responses) > 0 {
		if m.callIndex < len(m.responses) {
			text = m.responses[m.callIndex]
			m.callIndex++
		} else {
			text = m.responses[len(m.responses)-1]
		}
	}

	if len(m.toolCalls) > 0 {
		return &llm.GenerateResponse{
			Text:         text,
			ToolCalls:    m.toolCalls,
			FinishReason: llm.FinishReasonTool,
			Usage:        m.usage,
		}, nil
	}

	return &llm.GenerateResponse{
		Text:         text,
		FinishReason: llm.FinishReasonStop,
		Usage:        m.usage,
	}, nil
}

// Stream implements the llm.LLM interface, splitting responses into token deltas.
func (m *Model) Stream(ctx context.Context, req llm.GenerateRequest) (llm.Stream, error) {
	// Respect context cancellation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the request for testing inspection
	m.calls = append(m.calls, req)

	// If a custom stream handler is set, delegate to it
	if m.customStreamFn != nil {
		return m.customStreamFn(ctx, req)
	}

	text := ""
	if len(m.responses) > 0 {
		if m.callIndex < len(m.responses) {
			text = m.responses[m.callIndex]
			m.callIndex++
		} else {
			text = m.responses[len(m.responses)-1]
		}
	}

	chunks := splitIntoChunks(text, m.usage)
	if len(m.toolCalls) > 0 && len(chunks) > 0 {
		last := chunks[len(chunks)-1]
		last.ToolCalls = m.toolCalls
		last.FinishReason = llm.FinishReasonTool
	}
	return llm.NewSliceStream(ctx, chunks), nil
}

// splitIntoChunks splits a full response text into word-like delta chunks
// while preserving exact whitespace, newlines, and attached terminal metadata.
func splitIntoChunks(text string, usage llm.Usage) []*llm.StreamChunk {
	if text == "" {
		return []*llm.StreamChunk{
			{
				Delta:        "",
				FinishReason: llm.FinishReasonStop,
				Usage:        &usage,
			},
		}
	}

	var chunks []*llm.StreamChunk
	var current strings.Builder

	for i, r := range text {
		current.WriteRune(r)
		if r == ' ' || r == '\n' || r == '\t' || i == len(text)-1 {
			chunks = append(chunks, &llm.StreamChunk{
				Delta: current.String(),
			})
			current.Reset()
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, &llm.StreamChunk{
			Delta: current.String(),
		})
	}

	if len(chunks) > 0 {
		last := chunks[len(chunks)-1]
		last.FinishReason = llm.FinishReasonStop
		last.Usage = &usage
	}

	return chunks
}

// Calls returns a copy of all requests received by this mock.
func (m *Model) Calls() []llm.GenerateRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copied := make([]llm.GenerateRequest, len(m.calls))
	copy(copied, m.calls)
	return copied
}

// LastCall returns the most recent llm.GenerateRequest received, or nil if no calls were made.
func (m *Model) LastCall() *llm.GenerateRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.calls) == 0 {
		return nil
	}
	last := m.calls[len(m.calls)-1]
	return &last
}

// CallCount returns how many times Generate or Stream has been invoked.
func (m *Model) CallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.calls)
}

// SetToolCalls dynamically configures tool calls returned on future generations.
func (m *Model) SetToolCalls(calls ...llm.ToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = calls
}

// Reset clears recorded calls and resets the response index.
func (m *Model) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callIndex = 0
	m.calls = make([]llm.GenerateRequest, 0)
	m.toolCalls = nil
}
