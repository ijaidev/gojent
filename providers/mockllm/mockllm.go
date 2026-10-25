// Package mockllm provides a deterministic in-memory model for tests,
// examples, and local simulation.
package mockllm

import (
	"context"
	"encoding/json"
	"fmt"
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
	reasoning      string
	toolCalls      []llm.ToolCall
	provider       string
	providerValue  any
	features       llm.Features
}

var (
	_ llm.Model    = (*Model)(nil)
	_ llm.Streamer = (*Model)(nil)
)

// Option configures a Model.
type Option func(*Model)

// DefaultFeatures is the capability set a mock model advertises. It mirrors a
// fully featured adapter so agent capability checks pass.
func DefaultFeatures() llm.Features {
	return llm.Features{
		Streaming:         true,
		Tools:             true,
		ParallelToolCalls: true,
		StrictJSONSchema:  true,
		Reasoning:         true,
		ReasoningSummary:  true,
		Vision:            true,
		AudioIn:           true,
		Video:             true,
		Documents:         true,
		PromptCaching:     llm.CacheModeAutomatic,
	}
}

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

// WithReasoning configures a reasoning trace emitted before the text response.
func WithReasoning(text string) Option {
	return func(m *Model) { m.reasoning = text }
}

// WithToolCalls configures tool calls returned on generation.
func WithToolCalls(calls ...llm.ToolCall) Option {
	return func(m *Model) { m.toolCalls = cloneCalls(calls) }
}

// WithProviderMetadata attaches provider metadata to the first response part.
// It exercises the per-part replay-state path in the shared conformance suite.
func WithProviderMetadata(provider string, value any) Option {
	return func(m *Model) {
		m.provider = provider
		m.providerValue = value
	}
}

// WithFeatures overrides the advertised capability set.
func WithFeatures(features llm.Features) Option {
	return func(m *Model) { m.features = features }
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
		features: DefaultFeatures(),
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

// Info reports static model metadata.
func (m *Model) Info() llm.Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return llm.Info{
		Provider: "mock",
		Model:    m.modelID,
		Limits: llm.Limits{
			ContextWindow: 128_000,
			MaxOutput:     4_096,
		},
		Features: m.features,
	}
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
	reasoning := m.reasoning
	toolCalls := cloneCalls(m.toolCalls)
	provider, providerValue := m.provider, m.providerValue
	modelID := m.modelID
	index := m.callIndex
	m.mu.Unlock()

	if custom != nil {
		return custom(ctx, req)
	}

	parts := buildParts(text, reasoning, toolCalls)
	if len(parts) == 0 {
		parts = []llm.Part{llm.NewTextPart("Mock LLM response")}
	}
	if provider != "" {
		if err := parts[0].Meta().Set(provider, providerValue); err != nil {
			return nil, fmt.Errorf("mockllm: set provider metadata: %w", err)
		}
	}
	message := llm.AssistantMessageWithParts(parts...)
	message.Status = llm.MessageStatusCompleted

	finish := llm.FinishReasonStop
	if len(toolCalls) > 0 {
		finish = llm.FinishReasonTool
	}
	return &llm.Response{
		ID:              fmt.Sprintf("mock-%d", index),
		Model:           modelID,
		Message:         message,
		Status:          llm.MessageStatusCompleted,
		FinishReason:    finish,
		RawFinishReason: rawFinishReason(finish),
		Usage:           usage,
	}, nil
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
	reasoning := m.reasoning
	toolCalls := cloneCalls(m.toolCalls)
	provider, providerValue := m.provider, m.providerValue
	modelID := m.modelID
	index := m.callIndex
	m.mu.Unlock()

	if custom != nil {
		return custom(ctx, req)
	}
	return llm.NewSliceStream(ctx, buildEvents(text, reasoning, toolCalls, usage, provider, providerValue, modelID, index)), nil
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

func rawFinishReason(reason llm.FinishReason) string {
	switch reason {
	case llm.FinishReasonTool:
		return "tool_calls"
	default:
		return string(reason)
	}
}

func buildParts(text, reasoning string, calls []llm.ToolCall) []llm.Part {
	parts := make([]llm.Part, 0, len(calls)+2)
	if reasoning != "" {
		parts = append(parts, llm.NewReasoningPart(reasoning))
	}
	if text != "" {
		parts = append(parts, llm.NewTextPart(text))
	}
	for _, call := range calls {
		parts = append(parts, llm.NewToolCallPart(call))
	}
	return parts
}

func buildEvents(text, reasoning string, calls []llm.ToolCall, usage llm.Usage, provider string, providerValue any, modelID string, index int) []llm.StreamEvent {
	if text == "" && reasoning == "" && len(calls) == 0 {
		text = "Mock LLM response"
	}
	events := []llm.StreamEvent{llm.MessageStart{
		ID:    fmt.Sprintf("mock-%d", index),
		Model: modelID,
	}}

	next := 0
	metadata := func(part llm.Part) llm.Part {
		if provider != "" {
			_ = part.Meta().Set(provider, providerValue)
		}
		return part
	}

	if reasoning != "" {
		slot := next
		next++
		events = append(events, llm.PartStart{Index: slot, Part: metadata(llm.NewReasoningPart(""))})
		for _, fragment := range fragments(reasoning) {
			events = append(events, llm.PartDelta{Index: slot, Kind: llm.PartTypeReasoning, Text: fragment})
		}
		events = append(events, llm.PartEnd{Index: slot})
	}
	if text != "" {
		slot := next
		next++
		events = append(events, llm.PartStart{Index: slot, Part: metadata(llm.NewTextPart(""))})
		for _, fragment := range fragments(text) {
			events = append(events, llm.PartDelta{Index: slot, Kind: llm.PartTypeText, Text: fragment})
		}
		events = append(events, llm.PartEnd{Index: slot})
	}
	for _, call := range calls {
		slot := next
		next++
		canonical := llm.NewToolCallPart(call)
		events = append(events, llm.PartStart{Index: slot, Part: metadata(llm.NewToolCallPart(llm.ToolCall{
			ItemID: canonical.ItemID,
			CallID: canonical.CallID,
			Name:   canonical.Name,
			Kind:   canonical.Kind,
			Status: canonical.Status,
		}))})
		if len(canonical.Input) > 0 {
			events = append(events, llm.PartDelta{
				Index: slot,
				Kind:  llm.PartTypeToolCall,
				Call: &llm.ToolCallDelta{
					ItemID:        canonical.ItemID,
					CallID:        canonical.CallID,
					Name:          canonical.Name,
					InputSnapshot: append(json.RawMessage(nil), canonical.Input...),
				},
			})
		}
		events = append(events, llm.PartEnd{Index: slot})
	}

	finish := llm.FinishReasonStop
	if len(calls) > 0 {
		finish = llm.FinishReasonTool
	}
	events = append(events, llm.MessageEnd{
		Status:          llm.MessageStatusCompleted,
		FinishReason:    finish,
		RawFinishReason: rawFinishReason(finish),
		Usage:           usage,
	})
	return events
}

// fragments splits text into word-sized pieces for streaming. Every fragment
// is non-empty and concatenating them returns the original text.
func fragments(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	var current strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)
		if r == ' ' || r == '\n' || r == '\t' || i == len(runes)-1 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
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
