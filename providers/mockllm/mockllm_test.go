package mockllm_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestModel_BasicResponses(t *testing.T) {
	mock := mockllm.New("first", "second")

	res1, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res1.Text != "first" {
		t.Errorf("expected 'first', got '%s'", res1.Text)
	}

	res2, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res2.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res2.Text)
	}

	// Should repeat the last response
	res3, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res3.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res3.Text)
	}
}

func TestModel_RespectsContextCancellation(t *testing.T) {
	mock := mockllm.New("canceled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := mock.Generate(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Error("expected error due to canceled context, got nil")
	}
}

func TestModel_Stream(t *testing.T) {
	expectedText := "Hello world from Gojent!"
	mock := mockllm.New(expectedText)

	req := llm.GenerateRequest{
		Messages: []llm.Message{
			llm.UserMessage("hi"),
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

func TestModel_Stream_ContextCanceled(t *testing.T) {
	mock := mockllm.New("canceled streaming")

	// Pre-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mock.Stream(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Fatal("expected error with pre-canceled context, got nil")
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

func TestModel_Stream_CustomHandler(t *testing.T) {
	customStream := llm.NewSliceStream(context.Background(), []*llm.StreamChunk{
		{Delta: "custom1 "},
		{Delta: "custom2", FinishReason: llm.FinishReasonStop},
	})

	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomStreamHandler(func(_ context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
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

func TestModel_RecordsResponseFormat(t *testing.T) {
	strict := true
	rf := &llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Name:   "TestSchema",
		Schema: map[string]any{"type": "object"},
		Strict: &strict,
	}

	mock := mockllm.New(`{"name":"Gojent"}`)
	req := llm.GenerateRequest{
		Messages:       []llm.Message{llm.UserMessage("Send JSON")},
		ResponseFormat: rf,
	}
	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Text != `{"name":"Gojent"}` {
		t.Errorf("unexpected response text: %s", res.Text)
	}
	if mock.LastCall().ResponseFormat == nil || mock.LastCall().ResponseFormat.Name != "TestSchema" {
		t.Errorf("expected mock to record response format, got %+v", mock.LastCall().ResponseFormat)
	}
}

func TestModel_ToolCalls_GenerateAndStream(t *testing.T) {
	expectedCalls := []llm.ToolCall{
		{
			ID:        "call-1",
			Name:      "get_weather",
			Arguments: `{"location":"San Francisco"}`,
		},
	}

	mock := mockllm.New("tool calling...").ApplyOptions(
		mockllm.WithToolCalls(expectedCalls...),
	)

	req := llm.GenerateRequest{
		Messages: []llm.Message{llm.UserMessage("What is the weather?")},
		Tools: []llm.ToolDefinition{
			{Name: "get_weather", Description: "Fetches weather"},
		},
	}

	// 1. Generate test
	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.FinishReason != llm.FinishReasonTool {
		t.Errorf("expected finish reason tool_calls, got %s", res.FinishReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call-1" {
		t.Errorf("unexpected tool calls in response: %+v", res.ToolCalls)
	}

	// Verify Tools were recorded in request
	lastCall := mock.LastCall()
	if lastCall == nil || len(lastCall.Tools) != 1 || lastCall.Tools[0].Name != "get_weather" {
		t.Fatalf("expected recorded call to contain tools, got %+v", lastCall)
	}

	// 2. Stream test
	stream, err := mock.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var foundToolCall bool
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if len(chunk.ToolCalls) > 0 {
			foundToolCall = true
			if chunk.FinishReason != llm.FinishReasonTool {
				t.Errorf("expected stream chunk finish reason tool_calls, got %s", chunk.FinishReason)
			}
			if chunk.ToolCalls[0].Name != "get_weather" {
				t.Errorf("unexpected tool call name: %s", chunk.ToolCalls[0].Name)
			}
		}
	}
	if !foundToolCall {
		t.Error("expected tool call to be emitted in stream")
	}

	// 3. Reset clears tool calls
	mock.Reset()
	if mock.CallCount() != 0 {
		t.Errorf("expected call count 0 after reset, got %d", mock.CallCount())
	}
	normalRes, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate after reset failed: %v", err)
	}
	if normalRes.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected finish reason stop after reset, got %s", normalRes.FinishReason)
	}
}
