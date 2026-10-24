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

var (
	_ llm.Model    = (*mockllm.Model)(nil)
	_ llm.Streamer = (*mockllm.Model)(nil)
)

func TestModel_CyclesResponses(t *testing.T) {
	model := mockllm.New("first", "second")
	ctx := context.Background()

	for _, want := range []string{"first", "second", "second"} {
		resp, err := model.Generate(ctx, &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if resp.Message.Text() != want {
			t.Errorf("text = %q, want %q", resp.Message.Text(), want)
		}
		if resp.FinishReason != llm.FinishReasonStop {
			t.Errorf("finish = %q", resp.FinishReason)
		}
	}
	if model.CallCount() != 3 {
		t.Errorf("CallCount = %d, want 3", model.CallCount())
	}
}

func TestModel_DefaultResponseAndID(t *testing.T) {
	model := mockllm.New()
	if model.ModelID() != "mock-llm" {
		t.Errorf("ModelID = %q", model.ModelID())
	}
	resp, err := model.Generate(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.Text() == "" {
		t.Error("expected a default response")
	}
}

func TestModel_CustomOptions(t *testing.T) {
	usage := llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	model := mockllm.New("hello").
		ApplyOptions(mockllm.WithModelID("custom"), mockllm.WithUsage(usage))
	if model.ModelID() != "custom" {
		t.Errorf("ModelID = %q", model.ModelID())
	}

	calls := []llm.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":"go"}`}}
	toolModel := mockllm.New("").ApplyOptions(mockllm.WithToolCalls(calls...), mockllm.WithUsage(usage))
	resp, err := toolModel.Generate(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.FinishReason != llm.FinishReasonTool {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "c1" {
		t.Errorf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestModel_CustomHandlerIsNotDeadlocked(t *testing.T) {
	model := mockllm.New()
	model.ApplyOptions(mockllm.WithCustomHandler(func(_ context.Context, req *llm.Request) (*llm.Response, error) {
		// Calling back into the model must not deadlock.
		_ = model.CallCount()
		return &llm.Response{Message: llm.AssistantMessage("handled")}, nil
	}))

	resp, err := model.Generate(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("x")}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.Text() != "handled" {
		t.Errorf("text = %q", resp.Message.Text())
	}
	if model.CallCount() != 1 {
		t.Errorf("CallCount = %d", model.CallCount())
	}
}

func TestModel_CustomHandlerError(t *testing.T) {
	sentinel := errors.New("provider down")
	model := mockllm.New().ApplyOptions(mockllm.WithCustomHandler(func(context.Context, *llm.Request) (*llm.Response, error) {
		return nil, sentinel
	}))
	if _, err := model.Generate(context.Background(), &llm.Request{}); !errors.Is(err, sentinel) {
		t.Errorf("err = %v", err)
	}
}

func TestModel_StreamSplitsText(t *testing.T) {
	usage := llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	model := mockllm.New("hello world").ApplyOptions(mockllm.WithUsage(usage))
	stream, err := model.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var text strings.Builder
	var final *llm.StreamChunk
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		text.WriteString(chunk.Delta)
		final = chunk
	}
	if text.String() != "hello world" {
		t.Errorf("text = %q", text.String())
	}
	if final == nil || final.FinishReason != llm.FinishReasonStop {
		t.Errorf("final chunk = %+v", final)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", final.Usage)
	}
	if model.CallCount() != 1 {
		t.Errorf("CallCount = %d", model.CallCount())
	}
}

func TestModel_StreamToolCalls(t *testing.T) {
	calls := []llm.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{"q":"go"}`}}
	model := mockllm.New("thinking ").ApplyOptions(mockllm.WithToolCalls(calls...))
	stream, err := model.Stream(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var got []llm.ToolCall
	var finish llm.FinishReason
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		got = append(got, chunk.ToolCalls...)
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("tool calls = %+v", got)
	}
	if finish != llm.FinishReasonTool {
		t.Errorf("finish = %q", finish)
	}
}

func TestModel_CallsAreDeepCopies(t *testing.T) {
	model := mockllm.New("hello")
	req := &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}}
	if _, err := model.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req.Messages[0].Content[0].Text = "mutated after call"
	calls := model.Calls()
	if calls[0].Messages[0].Text() != "hi" {
		t.Errorf("recorded request was mutated: %q", calls[0].Messages[0].Text())
	}
	calls[0].Messages[0].Content[0].Text = "mutated copy"
	if model.Calls()[0].Messages[0].Text() != "hi" {
		t.Error("Calls returned an aliased request")
	}
}

func TestModel_Reset(t *testing.T) {
	model := mockllm.New("a", "b")
	ctx := context.Background()
	if _, err := model.Generate(ctx, &llm.Request{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	model.Reset()
	if model.CallCount() != 0 {
		t.Errorf("CallCount after Reset = %d", model.CallCount())
	}
	resp, err := model.Generate(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.Text() != "a" {
		t.Errorf("text after Reset = %q, want a", resp.Message.Text())
	}
}

func TestModel_ContextCancellation(t *testing.T) {
	model := mockllm.New("hello")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := model.Generate(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Generate err = %v", err)
	}
	if _, err := model.Stream(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Stream err = %v", err)
	}
}
