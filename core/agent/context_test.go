package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent"
)

func TestAgent_CallerDeadlineIsNotABudgetError(t *testing.T) {
	model := &scriptModel{steps: []step{{delay: 5 * time.Second}}}
	a := mustAgent(t, agent.Config{Name: "deadline", Model: model})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := a.Generate(ctx, "go")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if errors.Is(err, agent.ErrBudgetExceeded) {
		t.Errorf("caller deadline was reported as a budget error: %v", err)
	}
}

func TestAgent_DurationBudgetIsABudgetError(t *testing.T) {
	model := &scriptModel{steps: []step{{delay: 5 * time.Second}}}
	a := mustAgent(t, agent.Config{Name: "budget", Model: model})

	_, err := a.Generate(context.Background(), "go", agent.WithBudget(agent.Budget{
		MaxDuration: 100 * time.Millisecond,
	}))
	if !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
}

func TestAgent_CancellationCauseIsPreserved(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("unused")}}
	a := mustAgent(t, agent.Config{Name: "cancel", Model: model})

	cause := errors.New("tenant stopped the run")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)

	_, err := a.Generate(ctx, "go")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the cancellation cause", err)
	}
}
