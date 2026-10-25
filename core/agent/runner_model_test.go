package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
)

func TestAgent_ModelPanicIsContained(t *testing.T) {
	model := &scriptModel{steps: []step{{
		run: func(context.Context, *llm.Request) (*llm.Response, error) {
			panic("model exploded")
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "panic", Model: model})

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if resp != nil {
		t.Errorf("expected no response, got %+v", resp)
	}
	var panicErr *agent.PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("err = %T %v, want *agent.PanicError", err, err)
	}
	if !strings.Contains(panicErr.Error(), "model exploded") {
		t.Errorf("panic message = %q", panicErr.Error())
	}
}

func TestAgent_RequestDropsEmptyTextPartsFromInput(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("done")}}
	a := mustAgent(t, agent.Config{Name: "sanitize", Model: model})

	input := llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.Part{llm.NewTextPart(""), llm.NewTextPart("hi")},
	}
	if _, err := a.GenerateMessages(context.Background(), []llm.Message{input}); err != nil {
		t.Fatalf("GenerateMessages: %v", err)
	}

	messages := model.Calls()[0].Messages
	if len(messages) != 1 {
		t.Fatalf("request messages = %+v", messages)
	}
	if len(messages[0].Content) != 1 || messages[0].Text() != "hi" {
		t.Errorf("request content = %+v, want only %q", messages[0].Content, "hi")
	}
}

func TestAgent_NilResponseIsAnError(t *testing.T) {
	model := &scriptModel{steps: []step{{}}}
	a := mustAgent(t, agent.Config{Name: "empty", Model: model})

	if _, err := a.Generate(context.Background(), "go"); !errors.Is(err, agent.ErrResponseRequired) {
		t.Fatalf("err = %v, want ErrResponseRequired", err)
	}
}

func TestAgent_RetriesTransientModelFailures(t *testing.T) {
	model := &scriptModel{steps: []step{
		{err: &llm.Error{Kind: llm.KindRateLimit, RetryAfter: time.Millisecond}},
		finalText("recovered"),
	}}
	a := mustAgent(t, agent.Config{Name: "retry", Model: model})
	opts := agent.RunOptions{Retry: agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond}}

	resp, err := a.GenerateWith(context.Background(), "go", opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "recovered" || model.CallCount() != 2 {
		t.Errorf("Text = %q, calls = %d", resp.Text, model.CallCount())
	}
}

func TestAgent_DoesNotRetryInvalidRequests(t *testing.T) {
	model := &scriptModel{steps: []step{{err: &llm.Error{Kind: llm.KindInvalid}}}}
	a := mustAgent(t, agent.Config{Name: "invalid", Model: model})
	opts := agent.RunOptions{Retry: agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond}}

	_, err := a.GenerateWith(context.Background(), "go", opts)
	if err == nil || model.CallCount() != 1 {
		t.Fatalf("err = %v, calls = %d; want one non-retried call", err, model.CallCount())
	}
}
