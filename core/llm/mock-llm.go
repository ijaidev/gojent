package llm

import (
	"context"
	"sync"
)

// MockLLM is a thread-safe mock implementation of the LLM interface.
// It is designed for unit testing, integration tests, and local simulation
// without making external network calls.
type MockLLM struct {
	mu        sync.RWMutex
	modelID   string
	responses []string
	callIndex int
	calls     []GenerateRequest
	customFn  func(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
	usage     Usage
}

// MockOption configures a MockLLM.
type MockOption func(*MockLLM)

// WithModelID configures a custom model identifier on MockLLM.
func WithModelID(id string) MockOption {
	return func(m *MockLLM) {
		m.modelID = id
	}
}

// WithCustomHandler configures a dynamic response generator function.
func WithCustomHandler(fn func(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)) MockOption {
	return func(m *MockLLM) {
		m.customFn = fn
	}
}

// WithUsage configures custom token usage statistics returned on each response.
func WithUsage(u Usage) MockOption {
	return func(m *MockLLM) {
		m.usage = u
	}
}

// NewMockLLM creates a new MockLLM that returns the specified text responses.
//
// Usage:
//
//	// Returns "Hello, world!" for all Generate calls:
//	mock := llm.NewMockLLM("Hello, world!")
//
//	// Cycles sequentially through responses:
//	mock := llm.NewMockLLM("Answer 1", "Answer 2")
func NewMockLLM(texts ...string) *MockLLM {
	responses := texts
	if len(responses) == 0 {
		responses = []string{"Mock LLM response"}
	}
	return &MockLLM{
		modelID:   "mock-llm",
		responses: responses,
		calls:     make([]GenerateRequest, 0),
		usage: Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

// NewMockllm is an alias for NewMockLLM for compatibility with various casing.
func NewMockllm(texts ...string) *MockLLM {
	return NewMockLLM(texts...)
}

// ApplyOptions applies additional configuration options to the mock.
func (m *MockLLM) ApplyOptions(opts ...MockOption) *MockLLM {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ModelID returns the identifier of this mock model.
func (m *MockLLM) ModelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelID
}

// Generate implements the LLM interface.
func (m *MockLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
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

	return &GenerateResponse{
		Text:         text,
		FinishReason: FinishReasonStop,
		Usage:        m.usage,
	}, nil
}

// Calls returns a copy of all requests received by this mock.
func (m *MockLLM) Calls() []GenerateRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copied := make([]GenerateRequest, len(m.calls))
	copy(copied, m.calls)
	return copied
}

// LastCall returns the most recent GenerateRequest received, or nil if no calls were made.
func (m *MockLLM) LastCall() *GenerateRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.calls) == 0 {
		return nil
	}
	last := m.calls[len(m.calls)-1]
	return &last
}

// CallCount returns how many times Generate has been invoked.
func (m *MockLLM) CallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.calls)
}

// Reset clears recorded calls and resets the response index.
func (m *MockLLM) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callIndex = 0
	m.calls = make([]GenerateRequest, 0)
}
