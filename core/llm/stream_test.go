package llm_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestSliceStream_Close(t *testing.T) {
	stream := llm.NewSliceStream(context.Background(), []*llm.StreamChunk{
		{Delta: "chunk"},
	})

	err := stream.Close()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	_, err = stream.Recv()
	if !errors.Is(err, llm.ErrStreamClosed) {
		t.Errorf("expected ErrStreamClosed, got %v", err)
	}
}

func TestChannelStream(t *testing.T) {
	ch := make(chan *llm.StreamChunk, 2)
	errCh := make(chan error, 1)

	cancelCalled := false
	cancel := func() {
		cancelCalled = true
	}

	stream := llm.NewChannelStream(context.Background(), cancel, ch, errCh)

	ch <- &llm.StreamChunk{Delta: "part1"}
	ch <- &llm.StreamChunk{Delta: "part2"}
	close(ch)

	c1, err := stream.Recv()
	if err != nil || c1.Delta != "part1" {
		t.Fatalf("expected part1, got %v, err: %v", c1, err)
	}

	c2, err := stream.Recv()
	if err != nil || c2.Delta != "part2" {
		t.Fatalf("expected part2, got %v, err: %v", c2, err)
	}

	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	_ = stream.Close()
	if !cancelCalled {
		t.Error("expected cancel function to be called on Close()")
	}
}

func TestChannelStream_Error(t *testing.T) {
	ch := make(chan *llm.StreamChunk)
	errCh := make(chan error, 1)
	testErr := errors.New("network failure")
	errCh <- testErr

	stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
	defer func() {
		_ = stream.Close()
	}()

	_, err := stream.Recv()
	if !errors.Is(err, testErr) {
		t.Errorf("expected network failure error, got %v", err)
	}
}

func TestChannelStream_TerminalErrorSurvivesConcurrentClose(t *testing.T) {
	testErr := errors.New("provider stream failed")

	// The producer buffers its terminal error, then closes both channels.
	// Recv must surface the error even when the select observes the closed
	// chunk channel first; iterating many times covers both select branches.
	for i := 0; i < 500; i++ {
		ch := make(chan *llm.StreamChunk)
		errCh := make(chan error, 1)
		errCh <- testErr
		close(errCh)
		close(ch)

		stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
		_, err := stream.Recv()
		_ = stream.Close()
		if !errors.Is(err, testErr) {
			t.Fatalf("iteration %d: expected terminal error, got %v", i, err)
		}
	}
}

func TestChannelStream_CleanCloseReturnsEOF(t *testing.T) {
	ch := make(chan *llm.StreamChunk)
	errCh := make(chan error, 1)
	close(errCh)
	close(ch)

	stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
	defer func() { _ = stream.Close() }()

	for i := 0; i < 50; i++ {
		if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("iteration %d: expected io.EOF, got %v", i, err)
		}
	}
}
