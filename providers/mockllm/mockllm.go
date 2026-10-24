// Package mockllm provides a deterministic in-memory model for tests,
// examples, and local simulation.
package mockllm

import (
	"context"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

// Model is a thread-safe mock implementation of llm.Model and llm.Streamer.
type Model struct {
	mu             sync.RWMutex
	modelID        string
	responses      []string
	callIndex      int
	calls          []llm.Request
	customFn       func(ctx context.Context, req *llm.Request) (*llm.Response, error)
	customStreamFn func(ctx context.Context, req *llm.Request) (llm.Stream, error)
	usage          llm.Usage
	toolCalls      []llm.ToolCall
}

var (
	_ llm.Model    = (*Model)(nil)
	_ llm.Streamer = (*Model)(nil)
)

// Option configures a Model.
type Option func(*Model)

// WithModelID configures a custom model identifier.
func WithModelID(id string) Option {
	return func(m *Model) { m.modelID = id }
}

// WithCustomHandler configures a dynamic generate handler.
func WithCustomHandler(fn func(ctx context.Context, req *llm.Request) (*llm.Response, error)) Option {
	return func(m *Model) { m.customFn = fn }
}

// WithCustomStreamHandler configures a dynamic stream handler.
func WithCustomStreamHandler(fn func(ctx context.Context, req *llm.Request) (llm.Stream, error)) Option {
	return func(m *Model) { m.customStreamFn = fn }
}

// WithUsage configures token usage.
func WithUsage(usage llm.Usage) Option {
	return func(m *Model) { m.usage = usage }
}

// WithToolCalls configures tool calls returned on generation.
func WithToolCalls(calls ...llm.ToolCall) Option {
	return func(m *Model) { m.toolCalls = calls }
}

// New creates a Model that cycles through the supplied text responses.
func New(texts ...string) *Model {
	responses := texts
	if len(responses) == 0 {
		responses = []string{"Mock LLM response"}
	}
	return &Model{
		modelID:   "mock-llm",
		responses: responses,
		calls:     make([]llm.Request, 0),
		usage: llm.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

// ApplyOptions applies configuration options.
func (m *Model) ApplyOptions(opts ...Option) *Model {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ModelID returns the mock model identifier.
func (m *Model) ModelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelID
}

// Generate implements llm.Model.
func (m *Model) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.calls = append(m.calls, cloneRequest(req))
	custom := m.customFn
	var text string
	if custom == nil {
		text = m.nextText()
	}
	usage := m.usage
	toolCalls := cloneCalls(m.toolCalls)
	m.mu.Unlock()

	if custom != nil {
		return custom(ctx, req)
	}
	message := llm.Message{Role: llm.RoleAssistant}
	if text != "" {
		message.Content = []llm.Part{llm.TextPart(text)}
	}
	finish := llm.FinishReasonStop
	if len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
		finish = llm.FinishReasonTool
	}
	return &llm.Response{Message: message, FinishReason: finish, Usage: usage}, nil
}

// Stream implements llm.Streamer.
func (m *Model) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.calls = append(m.calls, cloneRequest(req))
	custom := m.customStreamFn
	var text string
	if custom == nil {
		text = m.nextText()
	}
	usage := m.usage
	toolCalls := cloneCalls(m.toolCalls)
	m.mu.Unlock()

	if custom != nil {
		return custom(ctx, req)
	}
	chunks := splitIntoChunks(text, usage)
	if len(toolCalls) > 0 {
		if len(chunks) == 0 {
			chunks = append(chunks, &llm.StreamChunk{})
		}
		last := chunks[len(chunks)-1]
		last.ToolCalls = toolCalls
		last.FinishReason = llm.FinishReasonTool
	}
	return llm.NewSliceStream(ctx, chunks), nil
}

func (m *Model) nextText() string {
	if len(m.responses) == 0 {
		return ""
	}
	if m.callIndex < len(m.responses) {
		text := m.responses[m.callIndex]
		m.callIndex++
		return text
	}
	return m.responses[len(m.responses)-1]
}

func splitIntoChunks(text string, usage llm.Usage) []*llm.StreamChunk {
	if text == "" {
		return []*llm.StreamChunk{{
			FinishReason: llm.FinishReasonStop,
			Usage:        &usage,
		}}
	}

	var chunks []*llm.StreamChunk
	var current strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)
		if r == ' ' || r == '\n' || r == '\t' || i == len(runes)-1 {
			chunks = append(chunks, &llm.StreamChunk{Delta: current.String()})
			current.Reset()
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, &llm.StreamChunk{Delta: current.String()})
	}
	if len(chunks) > 0 {
		last := chunks[len(chunks)-1]
		last.FinishReason = llm.FinishReasonStop
		last.Usage = &usage
	}
	return chunks
}

// Calls returns deep copies of the recorded requests.
func (m *Model) Calls() []llm.Request {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]llm.Request, len(m.calls))
	for i := range m.calls {
		out[i] = *m.calls[i].Clone()
	}
	return out
}

// LastCall returns a copy of the most recent request, or nil.
func (m *Model) LastCall() *llm.Request {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1].Clone()
}

// CallCount returns the number of Generate or Stream calls.
func (m *Model) CallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.calls)
}

// SetToolCalls configures tool calls for future generations.
func (m *Model) SetToolCalls(calls ...llm.ToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = cloneCalls(calls)
}

// Reset clears recorded calls and the response index.
func (m *Model) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callIndex = 0
	m.calls = make([]llm.Request, 0)
	m.toolCalls = nil
}

func cloneRequest(req *llm.Request) llm.Request {
	if req == nil {
		return llm.Request{}
	}
	return *req.Clone()
}

func cloneCalls(calls []llm.ToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, len(calls))
	copy(out, calls)
	return out
}
