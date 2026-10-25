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

func TestAgent_OptionsGeneration(t *testing.T) {
	temperature := 0.25
	maxTokens := 77
	topP := 0.8
	model := &scriptModel{steps: []step{finalText("first"), finalText("second")}}
	a := mustAgent(t, agent.Config{
		Name:           "assistant",
		Model:          model,
		Thinking:       &llm.ThinkingConfig{Level: llm.ThinkingLevelHigh},
		ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSON, Name: "out"},
		Temperature:    &temperature,
		MaxTokens:      &maxTokens,
		TopP:           &topP,
		ToolChoice:     llm.ToolChoice{Mode: llm.ToolChoiceRequired},
	})

	if _, err := a.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	request := model.Calls()[0]
	if request.Temperature == nil || *request.Temperature != temperature {
		t.Errorf("Temperature = %v, want %v", request.Temperature, temperature)
	}
	if request.MaxTokens == nil || *request.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %v, want %v", request.MaxTokens, maxTokens)
	}
	if request.TopP == nil || *request.TopP != topP {
		t.Errorf("TopP = %v, want %v", request.TopP, topP)
	}
	if request.Thinking == nil || request.Thinking.Level != llm.ThinkingLevelHigh {
		t.Errorf("Thinking = %+v, want high", request.Thinking)
	}
	if request.ResponseFormat == nil || request.ResponseFormat.Type != llm.ResponseFormatJSON {
		t.Errorf("ResponseFormat = %+v, want JSON", request.ResponseFormat)
	}
	if request.ToolChoice.Mode != llm.ToolChoiceRequired {
		t.Errorf("ToolChoice = %+v, want required", request.ToolChoice)
	}

	if _, err := a.Generate(context.Background(), "again", agent.WithToolChoice(llm.ToolChoice{Mode: llm.ToolChoiceNone})); err != nil {
		t.Fatalf("Generate override: %v", err)
	}
	if got := model.Calls()[1].ToolChoice.Mode; got != llm.ToolChoiceNone {
		t.Errorf("overridden ToolChoice = %q, want none", got)
	}
}

func TestAgent_OptionsBudgetMergesPerField(t *testing.T) {
	model := &scriptModel{steps: []step{{
		response: &llm.Response{
			Message:      llm.AssistantMessage("hi"),
			FinishReason: llm.FinishReasonStop,
			Usage:        llm.Usage{TotalTokens: 10},
		},
	}}}
	a := mustAgent(t, agent.Config{
		Name:   "assistant",
		Model:  model,
		Budget: agent.Budget{MaxTotalTokens: 5, MaxDuration: time.Minute},
	})

	response, err := a.Generate(context.Background(), "hi", agent.WithMaxSteps(2))
	if !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("Generate err = %v, want ErrBudgetExceeded", err)
	}
	if response == nil {
		t.Fatal("Generate returned no partial response")
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
}

func TestAgent_OptionsBudgetKeepsStepsWhenRunOverridesAnotherField(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call-1", Name: "missing", Arguments: "{}"}),
	}}
	a := mustAgent(t, agent.Config{
		Name:   "assistant",
		Model:  model,
		Budget: agent.Budget{MaxSteps: 1},
	})

	_, err := a.Generate(context.Background(), "hi", agent.WithBudget(agent.Budget{MaxTotalTokens: 100}))
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("Generate err = %v, want ErrMaxSteps", err)
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
}

func TestAgent_OptionsRetry(t *testing.T) {
	model := &scriptModel{steps: []step{
		{err: &llm.Error{Kind: llm.KindRateLimit}},
		finalText("recovered"),
	}}
	a := mustAgent(t, agent.Config{
		Name:  "assistant",
		Model: model,
		Retry: agent.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})

	response, err := a.Generate(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response.Text != "recovered" {
		t.Errorf("Text = %q, want recovered", response.Text)
	}
	if model.CallCount() != 2 {
		t.Errorf("model calls = %d, want 2", model.CallCount())
	}
}

func TestAgent_RunRetryOverridesOptions(t *testing.T) {
	model := &scriptModel{steps: []step{
		{err: &llm.Error{Kind: llm.KindRateLimit}},
		finalText("unused"),
	}}
	a := mustAgent(t, agent.Config{
		Name:  "assistant",
		Model: model,
		Retry: agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})

	_, err := a.Generate(context.Background(), "hi", agent.WithRetry(agent.RetryPolicy{MaxAttempts: 1}))
	if err == nil {
		t.Fatal("Generate succeeded, want retryable error")
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
}

func newDeadlineProbe(t *testing.T, seen *bool) tool.Tool {
	t.Helper()
	return tool.MustNew(tool.Config[struct{}, string]{
		ID: "probe",
		Execute: func(ctx context.Context, _ struct{}) (string, error) {
			_, *seen = ctx.Deadline()
			return "ok", nil
		},
	})
}

func toolProbeModel() *scriptModel {
	return &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call-1", Name: "probe", Arguments: "{}"}),
		finalText("done"),
	}}
}

func TestAgent_OptionsToolTimeout(t *testing.T) {
	var hadDeadline bool
	a := mustAgent(t, agent.Config{
		Name:        "assistant",
		Model:       toolProbeModel(),
		Tools:       []tool.Tool{newDeadlineProbe(t, &hadDeadline)},
		ToolTimeout: time.Minute,
	})

	if _, err := a.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !hadDeadline {
		t.Error("tool saw no deadline, want the agent timeout")
	}
}

func TestAgent_RunToolTimeoutDisablesOption(t *testing.T) {
	var hadDeadline bool
	a := mustAgent(t, agent.Config{
		Name:        "assistant",
		Model:       toolProbeModel(),
		Tools:       []tool.Tool{newDeadlineProbe(t, &hadDeadline)},
		ToolTimeout: time.Minute,
	})

	if _, err := a.Generate(context.Background(), "hi", agent.WithToolTimeout(-1)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if hadDeadline {
		t.Error("tool saw a deadline, want the timeout disabled")
	}
}

func TestAgent_OptionsApprover(t *testing.T) {
	recorder := &echoRecorder{}
	approved := 0
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call-1", Name: "echo", Arguments: `{"value":"hi"}`}),
		finalText("done"),
	}}
	a := mustAgent(t, agent.Config{
		Name:  "assistant",
		Model: model,
		Tools: []tool.Tool{newEchoTool(t, "echo", recorder)},
		Policy: agent.Hooks{
			OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
				return agent.Ask, nil
			},
		},
		Approver: agent.ApproverFunc(func(context.Context, agent.ToolRequest) (bool, error) {
			approved++
			return true, nil
		}),
	})

	response, err := a.Generate(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if approved != 1 {
		t.Errorf("approver calls = %d, want 1", approved)
	}
	if calls := recorder.Calls(); len(calls) != 1 {
		t.Errorf("tool calls = %d, want 1", len(calls))
	}
	if response.Text != "done" {
		t.Errorf("Text = %q, want done", response.Text)
	}
}

func TestAgent_OptionsStoreUsesRunSession(t *testing.T) {
	store := &recordingStore{}
	model := &scriptModel{steps: []step{finalText("ok")}}
	a := mustAgent(t, agent.Config{
		Name:  "assistant",
		Model: model,
		Store: store,
	})

	if _, err := a.Generate(context.Background(), "hi"); !errors.Is(err, agent.ErrSessionRequired) {
		t.Fatalf("Generate without session err = %v, want ErrSessionRequired", err)
	}
	if model.CallCount() != 0 {
		t.Errorf("model calls = %d, want 0", model.CallCount())
	}

	if _, err := a.Generate(context.Background(), "hi", agent.WithSession("conversation", "tenant")); err != nil {
		t.Fatalf("Generate with session: %v", err)
	}

	messages := store.Messages()
	if len(messages) != 2 {
		t.Fatalf("stored messages = %d, want 2", len(messages))
	}
	if messages[0].Role != llm.RoleUser || messages[0].Text() != "hi" {
		t.Errorf("first stored message = %+v, want user hi", messages[0])
	}
	if messages[1].Role != llm.RoleAssistant || messages[1].Text() != "ok" {
		t.Errorf("second stored message = %+v, want assistant ok", messages[1])
	}
}

func TestAgent_RunStoreOverridesOption(t *testing.T) {
	agentStore := &recordingStore{}
	runStore := &recordingStore{}
	model := &scriptModel{steps: []step{finalText("ok")}}
	a := mustAgent(t, agent.Config{
		Name:  "assistant",
		Model: model,
		Store: agentStore,
	})

	if _, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{
		Session: &session.Session{ID: "conversation"},
		Store:   runStore,
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if messages := agentStore.Messages(); len(messages) != 0 {
		t.Errorf("agent store received %d messages, want 0", len(messages))
	}
	if messages := runStore.Messages(); len(messages) != 2 {
		t.Errorf("run store received %d messages, want 2", len(messages))
	}
}
