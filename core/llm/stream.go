// Package llm provides interfaces, message definitions, and provider abstractions
// for interacting with Large Language Models.
package llm

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync"
)

var (
	// ErrStreamClosed is returned when an operation is attempted on an already closed stream.
	ErrStreamClosed = errors.New("llm: stream is closed")
)

// StreamChunk represents an incremental delta emitted during streaming generation.
type StreamChunk struct {
	// Delta is the newly generated text fragment.
	Delta string `json:"delta"`
	// ToolCalls contains any tool calls requested by the model during generation.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// FinishReason explains why generation ended, populated on the terminal chunk.
	FinishReason FinishReason `json:"finish_reason,omitempty"`
	// Usage contains token consumption statistics, typically populated on the final chunk.
	Usage *Usage `json:"usage,omitempty"`
	// Raw contains provider-specific stream event payload if available.
	Raw any `json:"raw,omitempty"`
}

// Stream represents an active token stream from an LLM.
// It provides sequential access to incoming chunks and supports cleanup.
type Stream interface {
	// Recv returns the next StreamChunk from the stream.
	// It returns io.EOF when the stream is cleanly exhausted.
	Recv() (*StreamChunk, error)
	// Close releases any resources associated with the stream.
	Close() error
}

// StreamAll returns a range-over-func iterator for reading chunks from any Stream.
func StreamAll(s Stream) iter.Seq2[*StreamChunk, error] {
	return func(yield func(*StreamChunk, error) bool) {
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

// sliceStream is an in-memory implementation of Stream backed by a slice of chunks.
type sliceStream struct {
	mu     sync.Mutex
	ctx    context.Context
	chunks []*StreamChunk
	idx    int
	closed bool
}

// NewSliceStream returns a new Stream that emits the provided chunks in order.
// If ctx is cancelled before or during consumption, Recv returns ctx.Err().
func NewSliceStream(ctx context.Context, chunks []*StreamChunk) Stream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &sliceStream{
		ctx:    ctx,
		chunks: chunks,
	}
}

func (s *sliceStream) Recv() (*StreamChunk, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStreamClosed
	}

	if s.idx >= len(s.chunks) {
		return nil, io.EOF
	}

	chunk := s.chunks[s.idx]
	s.idx++
	return chunk, nil
}

func (s *sliceStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// channelStream implements Stream backed by a Go channel.
type channelStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     <-chan *StreamChunk
	errCh  <-chan error
	mu     sync.Mutex
	closed bool
}

// NewChannelStream creates a Stream backed by a channel of StreamChunks.
// Closing the stream invokes the optional cancel function to terminate background producers.
func NewChannelStream(ctx context.Context, cancel context.CancelFunc, ch <-chan *StreamChunk, errCh <-chan error) Stream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &channelStream{
		ctx:    ctx,
		cancel: cancel,
		ch:     ch,
		errCh:  errCh,
	}
}

func (s *channelStream) Recv() (*StreamChunk, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrStreamClosed
	}
	s.mu.Unlock()

	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case err, ok := <-s.errCh:
		if ok && err != nil {
			return nil, err
		}
		return s.recvChunk()
	case chunk, ok := <-s.ch:
		if !ok {
			return nil, io.EOF
		}
		return chunk, nil
	}
}

func (s *channelStream) recvChunk() (*StreamChunk, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case chunk, ok := <-s.ch:
		if !ok {
			return nil, io.EOF
		}
		return chunk, nil
	}
}

func (s *channelStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
