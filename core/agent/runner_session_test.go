package agent_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

func TestAgent_InjectsSessionIntoModelContext(t *testing.T) {
	var (
		got session.Session
		ok  bool
	)
	model := &scriptModel{steps: []step{{
		run: func(ctx context.Context, _ *llm.Request) (*llm.Response, error) {
			got, ok = session.FromContext(ctx)
			return &llm.Response{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReasonStop,
			}, nil
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "session", Model: model})

	want := &session.Session{ID: "conversation", ResourceID: "tenant-a"}
	if _, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{Session: want}); err != nil {
		t.Fatalf("GenerateWith: %v", err)
	}
	if !ok {
		t.Fatal("model context did not carry the session")
	}
	if got != *want {
		t.Fatalf("session = %+v, want %+v", got, *want)
	}
}

func TestAgent_NoSessionLeavesContextEmpty(t *testing.T) {
	var ok bool
	model := &scriptModel{steps: []step{{
		run: func(ctx context.Context, _ *llm.Request) (*llm.Response, error) {
			_, ok = session.FromContext(ctx)
			return &llm.Response{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReasonStop,
			}, nil
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "session", Model: model})

	if _, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{}); err != nil {
		t.Fatalf("GenerateWith: %v", err)
	}
	if ok {
		t.Fatal("model context carried a session without RunOptions.Session")
	}
}
