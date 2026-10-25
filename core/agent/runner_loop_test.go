package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_Generate(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("hello")}}
	a := mustAgent(t, agent.Config{
		Name:         "assistant",
		Instructions: "be brief",
		Model:        model,
	})

	resp, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("Text = %q, want hello", resp.Text)
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("Messages = %d, want system, user, assistant", len(resp.Messages))
	}
	if resp.Messages[0].Role != llm.RoleSystem || resp.Messages[0].Text() != "be brief" {
		t.Errorf("unexpected system message: %+v", resp.Messages[0])
	}

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	if len(calls[0].Messages) != 2 {
		t.Fatalf("request messages = %d, want system and user", len(calls[0].Messages))
	}
}

func TestAgent_MaxStepsKeepsTranscriptComplete(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{Name: "echo", Arguments: `{"value":"one"}`}),
		toolCallStep(llm.ToolCall{Name: "echo", Arguments: `{"value":"two"}`}),
		finalText("never reached"),
	}}
	recorder := &echoRecorder{}
	echo := newEchoTool(t, "echo", recorder)
	a := mustAgent(t, agent.Config{Name: "looper", Model: model, Tools: []tool.Tool{echo}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Budget:  agent.Budget{MaxSteps: 2},
		Session: &session.Session{ID: "s"},
	})
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if resp == nil {
		t.Fatal("expected partial response")
	}
	if resp.FinishReason != llm.FinishReasonLength {
		t.Errorf("FinishReason = %q, want length", resp.FinishReason)
	}
	if model.CallCount() != 2 {
		t.Errorf("model calls = %d, want 2", model.CallCount())
	}
	if len(recorder.Calls()) != 2 {
		t.Errorf("executed tools = %d, want 2", len(recorder.Calls()))
	}
	messages := store.Messages()
	if len(messages) != 5 {
		t.Fatalf("stored messages = %d, want user + 2*(assistant+tool)", len(messages))
	}
	assertNoDanglingToolCalls(t, messages)
}

func TestAgent_WithMaxStepsOption(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "one", Name: "echo", Arguments: `{"value":"one"}`}),
	}}
	echo := newEchoTool(t, "echo", nil)
	a := mustAgent(t, agent.Config{Name: "looper", Model: model, Tools: []tool.Tool{echo}})

	_, err := a.Generate(context.Background(), "go", agent.WithMaxSteps(1))
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
}

func TestAgent_CancellationPreservesCause(t *testing.T) {
	started := make(chan struct{})
	model := &scriptModel{steps: []step{{
		run: func(ctx context.Context, _ *llm.Request) (*llm.Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "cancel", Model: model})
	run, err := a.StreamWith(context.Background(), "go", agent.RunOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model was never called")
	}
	stop := errors.New("stop requested by test")
	run.Cancel(stop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := run.Wait(ctx)
	if !errors.Is(err, stop) {
		t.Fatalf("Wait err = %v, want cancellation cause", err)
	}
	if resp != nil {
		t.Errorf("expected no response, got %+v", resp)
	}

	for range run.Events() {
		// Drain so the dispatcher can close the channel.
	}
}

func TestAgent_PartialResponseKeepsLastAssistantText(t *testing.T) {
	model := &scriptModel{steps: []step{
		{response: &llm.Response{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				Content:   []llm.Part{llm.TextPart("checking")},
				Thinking:  "need data",
				ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"value":"x"}`}},
			},
			FinishReason: llm.FinishReasonTool,
		}},
		finalText("never reached"),
	}}
	a := mustAgent(t, agent.Config{Name: "partial", Model: model, Tools: []tool.Tool{newEchoTool(t, "echo", nil)}})

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Budget: agent.Budget{MaxSteps: 1},
	})
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if resp == nil {
		t.Fatal("expected a partial response")
	}
	if resp.Text != "checking" || resp.Thinking != "need data" {
		t.Errorf("Text/Thinking = %q / %q, want checking / need data", resp.Text, resp.Thinking)
	}
}

func TestAgent_TokenBudget(t *testing.T) {
	t.Run("final answer", func(t *testing.T) {
		response := finalText("done")
		response.response.Usage = llm.Usage{PromptTokens: 40, CompletionTokens: 60, TotalTokens: 100}
		model := &scriptModel{steps: []step{response}}
		a := mustAgent(t, agent.Config{Name: "tokens", Model: model})
		store := &recordingStore{}

		resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
			Store:   store,
			Budget:  agent.Budget{MaxTotalTokens: 50},
			Session: &session.Session{ID: "s"},
		})
		if !errors.Is(err, agent.ErrBudgetExceeded) {
			t.Fatalf("err = %v, want ErrBudgetExceeded", err)
		}
		if resp == nil || resp.Text != "done" {
			t.Fatalf("expected the final answer with the budget error, got %+v", resp)
		}
		if len(store.Messages()) != 2 {
			t.Errorf("stored messages = %d, want user and assistant", len(store.Messages()))
		}
	})

	t.Run("tool step stops before next model call", func(t *testing.T) {
		first := &llm.Response{
			Message:      llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"value":"x"}`}}},
			FinishReason: llm.FinishReasonTool,
			Usage:        llm.Usage{PromptTokens: 40, CompletionTokens: 60, TotalTokens: 100},
		}
		model := &scriptModel{steps: []step{{response: first}, finalText("never reached")}}
		echo := newEchoTool(t, "echo", nil)
		a := mustAgent(t, agent.Config{Name: "tokens", Model: model, Tools: []tool.Tool{echo}})
		store := &recordingStore{}

		_, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
			Store:   store,
			Budget:  agent.Budget{MaxTotalTokens: 50},
			Session: &session.Session{ID: "s"},
		})
		if !errors.Is(err, agent.ErrBudgetExceeded) {
			t.Fatalf("err = %v, want ErrBudgetExceeded", err)
		}
		if model.CallCount() != 1 {
			t.Errorf("model calls = %d, want 1", model.CallCount())
		}
		messages := store.Messages()
		if len(messages) != 3 {
			t.Fatalf("stored messages = %d, want user, assistant, result", len(messages))
		}
		assertNoDanglingToolCalls(t, messages)
	})
}
