package llm_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMockLLM_Stream(t *testing.T) {
	expectedText := "Hello world from Gojent!"
	mock := llm.NewMockLLM(expectedText)

	req := llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
		},
	}

	stream, err := mock.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var accumulated strings.Builder
	var lastChunk *llm.StreamChunk

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error reading chunk: %v", err)
		}
		accumulated.WriteString(chunk.Delta)
		lastChunk = chunk
	}

	if accumulated.String() != expectedText {
		t.Errorf("expected '%s', got '%s'", expectedText, accumulated.String())
	}

	if lastChunk == nil {
		t.Fatal("expected at least one chunk emitted")
	}
	if lastChunk.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected finish reason stop, got %s", lastChunk.FinishReason)
	}
	if lastChunk.Usage == nil || lastChunk.Usage.TotalTokens == 0 {
		t.Errorf("expected valid usage stats on terminal chunk, got %+v", lastChunk.Usage)
	}

	if mock.CallCount() != 1 {
		t.Errorf("expected 1 recorded call, got %d", mock.CallCount())
	}
	if mock.LastCall() == nil || len(mock.LastCall().Messages) != 1 {
		t.Errorf("expected recorded call to have 1 message")
	}
}

func TestMockLLM_Stream_ContextCancelled(t *testing.T) {
	mock := llm.NewMockLLM("cancelled streaming")

	// Pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mock.Stream(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Fatal("expected error with pre-cancelled context, got nil")
	}

	// Mid-stream cancellation
	ctx2, cancel2 := context.WithCancel(context.Background())
	stream, err := mock.Stream(ctx2, llm.GenerateRequest{})
	if err != nil {
		t.Fatalf("unexpected stream start error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	// Cancel context before second recv
	cancel2()
	_, err = stream.Recv()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestMockLLM_Stream_CustomHandler(t *testing.T) {
	customStream := llm.NewSliceStream(context.Background(), []*llm.StreamChunk{
		{Delta: "custom1 "},
		{Delta: "custom2", FinishReason: llm.FinishReasonStop},
	})

	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomStreamHandler(func(_ context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			return customStream, nil
		}),
	)

	stream, err := mock.Stream(context.Background(), llm.GenerateRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	c1, err := stream.Recv()
	if err != nil || c1.Delta != "custom1 " {
		t.Fatalf("expected custom1, got %v, err: %v", c1, err)
	}

	c2, err := stream.Recv()
	if err != nil || c2.Delta != "custom2" {
		t.Fatalf("expected custom2, got %v, err: %v", c2, err)
	}

	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

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

func TestStreamAll_Iterator(t *testing.T) {
	stream := llm.NewSliceStream(context.Background(), []*llm.StreamChunk{
		{Delta: "a"},
		{Delta: "b"},
		{Delta: "c"},
	})

	var result string
	for chunk, err := range llm.StreamAll(stream) {
		if err != nil {
			t.Fatalf("unexpected iterator error: %v", err)
		}
		result += chunk.Delta
	}

	if result != "abc" {
		t.Errorf("expected 'abc', got '%s'", result)
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
