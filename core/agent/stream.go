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

var (
	// ErrStreamNotFinished is returned when accessing the final agent response before the stream completes.
	ErrStreamNotFinished = errors.New("agent: stream has not finished yet")
	// ErrStreamClosed is returned when operations are attempted on a closed stream.
	ErrStreamClosed = errors.New("agent: stream is closed")
)

// StreamResponse manages the streaming lifecycle of an agent response,
// accumulating tokens, usage statistics, and conversation messages in real time.
type StreamResponse struct {
	mu           sync.Mutex
	ctx          context.Context
	stream       llm.Stream
	messages     []llm.Message
	accumulated  strings.Builder
	finishReason llm.FinishReason
	usage        llm.Usage
	closed       bool
	completed    bool
	err          error
}

// newStreamResponse initializes a new StreamResponse wrapping an underlying LLM stream.
func newStreamResponse(ctx context.Context, stream llm.Stream, messages []llm.Message) *StreamResponse {
	return &StreamResponse{
		ctx:          ctx,
		stream:       stream,
		messages:     messages,
		finishReason: llm.FinishReasonStop,
	}
}

// Recv returns the next incremental StreamChunk from the agent's LLM stream.
// It returns io.EOF when generation has completed normally.
func (s *StreamResponse) Recv() (*llm.StreamChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStreamClosed
	}
	if s.completed {
		return nil, io.EOF
	}
	if s.err != nil {
		return nil, s.err
	}

	chunk, err := s.stream.Recv()
	if errors.Is(err, io.EOF) {
		s.completed = true
		return nil, io.EOF
	}
	if err != nil {
		s.err = err
		return nil, err
	}

	s.accumulated.WriteString(chunk.Delta)
	if chunk.FinishReason != "" {
		s.finishReason = chunk.FinishReason
	}
	if chunk.Usage != nil {
		s.usage = *chunk.Usage
	}

	return chunk, nil
}

// Close closes the underlying stream and cancels any ongoing generation.
func (s *StreamResponse) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}

// Text returns the text accumulated from stream deltas so far.
func (s *StreamResponse) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accumulated.String()
}

// Response returns the full Agent response once the stream has completed.
// Returns ErrStreamNotFinished if called before the stream reaches EOF.
func (s *StreamResponse) Response() (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.completed {
		return nil, ErrStreamNotFinished
	}

	text := s.accumulated.String()
	fullMessages := make([]llm.Message, len(s.messages)+1)
	copy(fullMessages, s.messages)
	fullMessages[len(fullMessages)-1] = llm.Message{
		Role:    llm.RoleAssistant,
		Content: text,
	}

	return &Response{
		Text:         text,
		Messages:     fullMessages,
		FinishReason: s.finishReason,
		Usage:        s.usage,
	}, nil
}

// Drain reads all remaining chunks until the stream terminates and returns the accumulated Response.
func (s *StreamResponse) Drain() (*Response, error) {
	for {
		_, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return s.Response()
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
