package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

// panicStore proves that dependency panics cannot escape a run.
type panicStore struct {
	load bool
}

var _ session.Store = panicStore{}

func (s panicStore) Load(context.Context, session.Session) ([]llm.Message, error) {
	if s.load {
		panic("load panic")
	}
	return nil, nil
}

func (s panicStore) Append(context.Context, session.Session, ...llm.Message) error {
	panic("append panic")
}

func TestAgent_ContainsSynchronousStorePanic(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("unused")}}
	a := mustAgent(t, agent.Config{Name: "panic", Model: model})

	_, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   panicStore{load: true},
		Session: &session.Session{ID: "s"},
	})
	var panicErr *agent.PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("err = %v, want a PanicError", err)
	}
	if !strings.Contains(panicErr.Error(), "load panic") {
		t.Errorf("panic error = %v", panicErr)
	}
	if model.CallCount() != 0 {
		t.Errorf("model calls = %d, want 0", model.CallCount())
	}
}

func TestAgent_ContainsAsynchronousStorePanic(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("unused")}}
	a := mustAgent(t, agent.Config{Name: "panic", Model: model})

	run, err := a.StreamWith(context.Background(), "go", agent.RunOptions{
		Store:   panicStore{load: true},
		Session: &session.Session{ID: "s"},
	})
	if err != nil {
		t.Fatalf("StreamWith: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := run.Wait(ctx)
	if response != nil {
		t.Errorf("response = %+v, want nil", response)
	}
	var panicErr *agent.PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("Wait err = %v, want a PanicError", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range run.Events() {
			// Drain so the dispatcher can close the channel.
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event channel did not close after a panic")
	}
}
