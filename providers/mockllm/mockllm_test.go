package mockllm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
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
		if resp.Status != llm.MessageStatusCompleted {
			t.Errorf("status = %q", resp.Status)
		}
	}
	if model.CallCount() != 3 {
		t.Errorf("CallCount = %d, want 3", model.CallCount())
	}
}

func TestModel_DefaultResponseAndInfo(t *testing.T) {
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

	info := model.Info()
	if info.Provider != "mock" || info.Model != "mock-llm" {
		t.Errorf("info = %+v", info)
	}
	if !info.Features.Streaming || !info.Features.Tools || !info.Features.ParallelToolCalls {
		t.Errorf("features = %+v", info.Features)
	}
	if info.Limits.ContextWindow == 0 || info.Limits.MaxOutput == 0 {
		t.Errorf("limits = %+v", info.Limits)
	}
}

func TestModel_CustomOptions(t *testing.T) {
	usage := llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	model := mockllm.New("hello").
		ApplyOptions(mockllm.WithModelID("custom"), mockllm.WithUsage(usage))
	if model.ModelID() != "custom" {
		t.Errorf("ModelID = %q", model.ModelID())
	}

	calls := []llm.ToolCall{
		{ItemID: "item-1", CallID: "c1", Name: "lookup", Input: json.RawMessage(`{"q":"go"}`)},
	}
	toolModel := mockllm.New("").ApplyOptions(mockllm.WithToolCalls(calls...), mockllm.WithUsage(usage))
	resp, err := toolModel.Generate(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.FinishReason != llm.FinishReasonTool {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	got := resp.Message.ToolCalls()
	if len(got) != 1 || got[0].CallID != "c1" || got[0].ItemID != "item-1" {
		t.Errorf("tool calls = %+v", got)
	}
	if got[0].EffectiveKind() != llm.ToolKindFunction {
		t.Errorf("kind = %q", got[0].Kind)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestModel_ReasoningAndProviderMetadata(t *testing.T) {
	model := mockllm.New("answer").ApplyOptions(
		mockllm.WithReasoning("think"),
		mockllm.WithProviderMetadata("mock", map[string]string{"trace": "abc"}),
	)
	resp, err := model.Generate(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := llmtest.ReasoningText(resp.Message); got != "think" {
		t.Errorf("reasoning = %q", got)
	}
	var metadata map[string]string
	if err := resp.Message.Content[0].Meta().Get("mock", &metadata); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if metadata["trace"] != "abc" {
		t.Errorf("metadata = %+v", metadata)
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

func TestModel_StreamMatchesGenerate(t *testing.T) {
	usage := llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	model := mockllm.New("hello world").ApplyOptions(mockllm.WithUsage(usage))
	stream, err := model.Stream(context.Background(), &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp := llmtest.Collect(t, stream)
	if got := resp.Message.Text(); got != "hello world" {
		t.Errorf("text = %q", got)
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	if resp.Usage != usage {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if model.CallCount() != 1 {
		t.Errorf("CallCount = %d", model.CallCount())
	}
}

func TestModel_StreamToolCalls(t *testing.T) {
	calls := []llm.ToolCall{
		{CallID: "c1", Name: "lookup", Input: json.RawMessage(`{"q":"go"}`)},
		{CallID: "c2", Name: "clock", Input: json.RawMessage(`{}`)},
	}
	model := mockllm.New("thinking ").ApplyOptions(mockllm.WithToolCalls(calls...))
	stream, err := model.Stream(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp := llmtest.Collect(t, stream)
	got := resp.Message.ToolCalls()
	if len(got) != 2 || got[0].CallID != "c1" || got[1].CallID != "c2" {
		t.Errorf("tool calls = %+v", got)
	}
	if string(got[0].Input) != `{"q":"go"}` {
		t.Errorf("input = %s", got[0].Input)
	}
	if resp.FinishReason != llm.FinishReasonTool {
		t.Errorf("finish = %q", resp.FinishReason)
	}
}

func TestModel_CallsAreDeepCopies(t *testing.T) {
	model := mockllm.New("hello")
	req := &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}}
	if _, err := model.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req.Messages[0].Content[0].(*llm.TextPart).Text = "mutated after call"
	calls := model.Calls()
	if calls[0].Messages[0].Text() != "hi" {
		t.Errorf("recorded request was mutated: %q", calls[0].Messages[0].Text())
	}
	calls[0].Messages[0].Content[0].(*llm.TextPart).Text = "mutated copy"
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
