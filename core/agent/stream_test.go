package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
)

func TestAgent_Stream_WithMockLLM(t *testing.T) {
	mock := llm.NewMockLLM("Here is step one. Here is step two.")

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
	if res.Messages[0].Role != llm.RoleSystem || res.Messages[0].Content != "You are a concise assistant." {
		t.Errorf("expected system message, got %+v", res.Messages[0])
	}
	if res.Messages[1].Role != llm.RoleUser || res.Messages[1].Content != "give me the steps" {
		t.Errorf("expected user prompt, got %+v", res.Messages[1])
	}
	if res.Messages[2].Role != llm.RoleAssistant || res.Messages[2].Content != expectedText {
		t.Errorf("expected assistant reply, got %+v", res.Messages[2])
	}
	if res.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected stop finish reason, got %s", res.FinishReason)
	}
}

func TestAgent_Stream_Drain(t *testing.T) {
	mock := llm.NewMockLLM("Drained response")

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
	mock := llm.NewMockLLM("Alpha Beta Gamma")

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
	mock := llm.NewMockLLM("Some content")

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
	mock := llm.NewMockLLM("Content to be closed")

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
	defaultMock := llm.NewMockLLM("Default stream")
	overrideMock := llm.NewMockLLM("Overridden stream")

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
