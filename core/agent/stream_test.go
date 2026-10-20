package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestAgent_Stream_WithMockLLM(t *testing.T) {
	mock := mockllm.New("Here is step one. Here is step two.")

	bot, err := agent.New(agent.Config{
		ID:           "assistant",
		Name:         "Assistant Agent",
		Instructions: "You are a concise assistant.",
		Model:        mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()
	stream, err := bot.Stream(ctx, "give me the steps")
	if err != nil {
		t.Fatalf("agent.Stream returned error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var accumulated strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected chunk error: %v", err)
		}
		accumulated.WriteString(chunk.Delta)
	}

	expectedText := "Here is step one. Here is step two."
	if accumulated.String() != expectedText {
		t.Errorf("expected '%s', got '%s'", expectedText, accumulated.String())
	}
	if stream.Text() != expectedText {
		t.Errorf("expected stream.Text() '%s', got '%s'", expectedText, stream.Text())
	}

	// Verify the final Response after stream has concluded
	res, err := stream.Response()
	if err != nil {
		t.Fatalf("stream.Response returned error: %v", err)
	}
	if res.Text != expectedText {
		t.Errorf("expected res.Text '%s', got '%s'", expectedText, res.Text)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 conversation messages, got %d", len(res.Messages))
	}
	if res.Messages[0].Role != llm.RoleSystem || res.Messages[0].Text() != "You are a concise assistant." {
		t.Errorf("expected system message, got %+v", res.Messages[0])
	}
	if res.Messages[1].Role != llm.RoleUser || res.Messages[1].Text() != "give me the steps" {
		t.Errorf("expected user prompt, got %+v", res.Messages[1])
	}
	if res.Messages[2].Role != llm.RoleAssistant || res.Messages[2].Text() != expectedText {
		t.Errorf("expected assistant reply, got %+v", res.Messages[2])
	}
	if res.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected stop finish reason, got %s", res.FinishReason)
	}
}

func TestAgent_Stream_Drain(t *testing.T) {
	mock := mockllm.New("Drained response")

	bot, err := agent.New(agent.Config{
		ID:    "drain-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("drain returned error: %v", err)
	}
	if res.Text != "Drained response" {
		t.Errorf("expected 'Drained response', got '%s'", res.Text)
	}
}

func TestAgent_Stream_AllIterator(t *testing.T) {
	mock := mockllm.New("Alpha Beta Gamma")

	bot, err := agent.New(agent.Config{
		ID:    "iterator-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(context.Background(), "iterate")
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var gathered string
	for chunk, err := range stream.All() {
		if err != nil {
			t.Fatalf("unexpected iterator error: %v", err)
		}
		gathered += chunk.Delta
	}

	if gathered != "Alpha Beta Gamma" {
		t.Errorf("expected 'Alpha Beta Gamma', got '%s'", gathered)
	}

	res, err := stream.Response()
	if err != nil {
		t.Fatalf("expected valid response after iterator, got error: %v", err)
	}
	if res.Text != "Alpha Beta Gamma" {
		t.Errorf("expected 'Alpha Beta Gamma', got '%s'", res.Text)
	}
}

func TestAgent_Stream_PrematureResponseError(t *testing.T) {
	mock := mockllm.New("Some content")

	bot, err := agent.New(agent.Config{
		ID:    "premature-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	// Access response before stream completes
	_, err = stream.Response()
	if !errors.Is(err, agent.ErrStreamNotFinished) {
		t.Errorf("expected ErrStreamNotFinished, got %v", err)
	}
}

func TestAgent_Stream_Close(t *testing.T) {
	mock := mockllm.New("Content to be closed")

	bot, err := agent.New(agent.Config{
		ID:    "close-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	err = stream.Close()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	_, err = stream.Recv()
	if !errors.Is(err, agent.ErrStreamClosed) {
		t.Errorf("expected ErrStreamClosed, got %v", err)
	}
}

func TestAgent_Stream_PerCallModelOverride(t *testing.T) {
	defaultMock := mockllm.New("Default stream")
	overrideMock := mockllm.New("Overridden stream")

	bot, err := agent.New(agent.Config{
		ID:    "override-agent",
		Model: defaultMock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(context.Background(), "test", agent.WithModel(overrideMock))
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("unexpected drain error: %v", err)
	}
	if res.Text != "Overridden stream" {
		t.Errorf("expected 'Overridden stream', got '%s'", res.Text)
	}

	if defaultMock.CallCount() != 0 {
		t.Errorf("expected default mock not called, got %d", defaultMock.CallCount())
	}
	if overrideMock.CallCount() != 1 {
		t.Errorf("expected override mock called once, got %d", overrideMock.CallCount())
	}
}

func TestAgent_Stream_MaxStepsDoesNotLeaveDanglingToolCalls(t *testing.T) {
	ctx := context.Background()

	invocations := 0
	countTool := tool.MustNew(tool.Config[struct{}, string]{
		ID:          "count",
		Description: "Counts how many times it was invoked.",
		Execute: func(context.Context, struct{}) (string, error) {
			invocations++
			return "counted", nil
		},
	})

	// The model always asks for the tool, so only the ReAct budget stops it.
	mock := mockllm.New().ApplyOptions(mockllm.WithCustomStreamHandler(
		func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			return llm.NewSliceStream(ctx, []*llm.StreamChunk{
				{
					ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "count", Arguments: "{}"},
					},
					FinishReason: llm.FinishReasonTool,
				},
			}), nil
		},
	))

	bot, err := agent.New(agent.Config{
		ID:    "max-steps-agent",
		Model: mock,
		Tools: []tool.Tool{countTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := bot.Stream(ctx, "loop forever", agent.WithMaxSteps(2))
	if err != nil {
		t.Fatalf("agent.Stream returned error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("drain returned error: %v", err)
	}

	if res.FinishReason != llm.FinishReasonLength {
		t.Errorf("expected finish reason length, got %s", res.FinishReason)
	}
	if invocations != 2 {
		t.Errorf("expected the tool to run once per allowed step (2), got %d", invocations)
	}
	if last := res.Messages[len(res.Messages)-1]; last.Role != llm.RoleTool {
		t.Errorf("expected transcript to end with a tool result, got role %q", last.Role)
	}
	assertNoDanglingToolCalls(t, res.Messages)
}

// assertNoDanglingToolCalls verifies that every assistant tool call has a
// matching tool result message, i.e. the transcript is safe to replay to a
// strict provider API.
func assertNoDanglingToolCalls(t *testing.T, messages []llm.Message) {
	t.Helper()

	answered := make(map[string]bool)
	for i := range messages {
		if messages[i].Role == llm.RoleTool && messages[i].ToolCallID != "" {
			answered[messages[i].ToolCallID] = true
		}
	}
	for i := range messages {
		msg := &messages[i]
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			if !answered[call.ID] {
				t.Errorf("message %d: tool call %q (%s) has no tool result", i, call.ID, call.Name)
			}
		}
	}
}
