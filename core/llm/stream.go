package llm

import (
	"context"
	"encoding/json"
	"io"
	"sync"
)

// StreamEvent is one event in a canonical model stream. The interface is
// sealed; adapters emit these values and the runner consumes them.
type StreamEvent interface {
	isStreamEvent()
}

// MessageStart begins a streamed message.
type MessageStart struct {
	ID    string
	Model string
}

// PartStart begins one canonical content part. Index is the part index; 0 is
// valid.
type PartStart struct {
	Index int
	Part  Part
}

// PartDelta updates one canonical content part. Exactly one of Text or Call is
// normally set. Replace means the value is a full snapshot, not an increment.
type PartDelta struct {
	Index    int
	Kind     PartType
	Replace  bool
	Text     string
	Call     *ToolCallDelta
	Provider ProviderData
}

// PartEnd ends one canonical content part.
type PartEnd struct {
	Index int
}

// MessageEnd ends a stream with terminal status, usage, and finish reason.
type MessageEnd struct {
	Status          MessageStatus
	FinishReason    FinishReason
	RawFinishReason string
	Usage           Usage
}

// ProviderEvent carries a raw adapter event that has no canonical form.
type ProviderEvent struct {
	Provider string
	Raw      json.RawMessage
}

func (MessageStart) isStreamEvent()  {}
func (PartStart) isStreamEvent()     {}
func (PartDelta) isStreamEvent()     {}
func (PartEnd) isStreamEvent()       {}
func (MessageEnd) isStreamEvent()    {}
func (ProviderEvent) isStreamEvent() {}

// ToolCallDelta is a streamed update to a tool call. InputFragment appends to
// the accumulated JSON text; InputSnapshot replaces it.
type ToolCallDelta struct {
	ItemID        string
	CallID        string
	Name          string
	InputFragment string
	InputSnapshot json.RawMessage
}

// Stream is an active model stream. Recv returns io.EOF after the final
// MessageEnd event.
type Stream interface {
	// Recv returns the next event. It returns io.EOF when the stream is
	// cleanly exhausted.
	Recv() (StreamEvent, error)
	// Close releases resources. Closing an already closed stream is a no-op.
	Close() error
}

// NewSliceStream returns a Stream over events. If ctx is canceled before or
// during consumption, Recv returns ctx.Err().
func NewSliceStream(ctx context.Context, events []StreamEvent) Stream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &sliceStream{ctx: ctx, events: events}
}

type sliceStream struct {
	mu     sync.Mutex
	ctx    context.Context
	events []StreamEvent
	index  int
	closed bool
}

func (s *sliceStream) Recv() (StreamEvent, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStreamClosed
	}
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *sliceStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// NewChannelStream creates a Stream backed by a channel of events.
//
// Producers must send any terminal error to errCh before closing ch. When both
// channels are ready, a pending error wins over the clean io.EOF signaled by a
// closed event channel.
func NewChannelStream(ctx context.Context, cancel context.CancelFunc, ch <-chan StreamEvent, errCh <-chan error) Stream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &channelStream{ctx: ctx, cancel: cancel, ch: ch, errCh: errCh}
}

type channelStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     <-chan StreamEvent
	errCh  <-chan error
	mu     sync.Mutex
	closed bool
}

func (s *channelStream) Recv() (StreamEvent, error) {
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
		return s.recvEvent()
	case event, ok := <-s.ch:
		if !ok {
			return nil, s.terminalErr()
		}
		return event, nil
	}
}

func (s *channelStream) recvEvent() (StreamEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case event, ok := <-s.ch:
		if !ok {
			return nil, s.terminalErr()
		}
		return event, nil
	}
}

// terminalErr prefers a buffered terminal error over a closed event channel.
func (s *channelStream) terminalErr() error {
	select {
	case err, ok := <-s.errCh:
		if ok && err != nil {
			return err
		}
	default:
	}
	return io.EOF
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
