package agent

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

// StreamResponse manages the streaming lifecycle of an agent response,
// executing tools across ReAct iterations and accumulating tokens, deltas,
// and messages in real time.
type StreamResponse struct {
	mu              sync.Mutex
	ctx             context.Context
	run             *run
	stream          llm.Stream
	messages        []llm.Message
	req             llm.GenerateRequest
	step            int
	stepAccumulated strings.Builder
	stepThinking    strings.Builder
	stepToolCalls   []llm.ToolCall
	stepUsage       llm.Usage
	stepRaw         any
	allToolCalls    []llm.ToolCall
	finishReason    llm.FinishReason
	usage           llm.Usage
	closed          bool
	completed       bool
	err             error
}

// newStreamResponse initializes a new StreamResponse managing multi-step ReAct streaming.
func newStreamResponse(ctx context.Context, r *run, req llm.GenerateRequest, stream llm.Stream) *StreamResponse {
	return &StreamResponse{
		ctx:          ctx,
		run:          r,
		stream:       stream,
		messages:     r.messages,
		req:          req,
		finishReason: llm.FinishReasonStop,
	}
}

// Close closes the underlying stream and cancels any ongoing generation.
func (s *StreamResponse) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	if s.stream != nil {
		return s.stream.Close()
	}
	return nil
}

// Text returns the text accumulated from stream deltas for the final generation turn.
func (s *StreamResponse) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepAccumulated.String()
}

// Thinking returns the thinking/reasoning text accumulated from stream deltas for the final generation turn.
func (s *StreamResponse) Thinking() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepThinking.String()
}

// ToolCalls returns a copy of all tool calls received across all ReAct steps.
func (s *StreamResponse) ToolCalls() []llm.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := append([]llm.ToolCall{}, s.allToolCalls...)
	if len(s.stepToolCalls) > 0 {
		total = append(total, s.stepToolCalls...)
	}
	return total
}

// Response returns the full Agent response once the stream has completed all ReAct iterations.
// Returns ErrStreamNotFinished if called before the stream reaches EOF.
func (s *StreamResponse) Response() (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.completed {
		return nil, ErrStreamNotFinished
	}

	text := s.stepAccumulated.String()
	toolCalls := s.allToolCalls
	if len(toolCalls) == 0 && len(s.stepToolCalls) > 0 {
		toolCalls = s.stepToolCalls
	}

	return &Response{
		Text:         text,
		Thinking:     s.stepThinking.String(),
		ToolCalls:    toolCalls,
		Messages:     s.messages,
		FinishReason: s.finishReason,
		Usage:        s.usage,
	}, nil
}

// All returns a range-over-func iterator for reading chunks using for ... range loops.
func (s *StreamResponse) All() iter.Seq2[*llm.StreamChunk, error] {
	return func(yield func(*llm.StreamChunk, error) bool) {
		for {
			chunk, err := s.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}
