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

type stubPolicy struct{}

func (*stubPolicy) BeforeModel(context.Context, *agent.ModelRequest) error { return nil }
func (*stubPolicy) AfterModel(context.Context, *agent.ModelResult) error   { return nil }
func (*stubPolicy) BeforeTool(context.Context, *agent.ToolRequest) (agent.Decision, error) {
	return agent.Allow, nil
}
func (*stubPolicy) AfterTool(context.Context, *agent.ToolResult) error { return nil }

type stubApprover struct{}

func (*stubApprover) Approve(context.Context, agent.ToolRequest) (bool, error) {
	return true, nil
}

func TestAgent_ApplyAgentOptions(t *testing.T) {
	temperature := 0.4
	maxTokens := 64
	topP := 0.7
	model := &scriptModel{}
	policy := &stubPolicy{}
	approver := &stubApprover{}
	store := &recordingStore{}
	a := mustAgent(t, agent.Config{
		Name:           "assistant",
		Model:          model,
		Policy:         policy,
		Approver:       approver,
		Store:          store,
		Budget:         agent.Budget{MaxSteps: 4, MaxToolCalls: 2, MaxTotalTokens: 100, MaxDuration: time.Minute},
		Retry:          agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute},
		ToolTimeout:    30 * time.Second,
		Thinking:       &llm.ThinkingConfig{Level: llm.ThinkingLevelHigh},
		ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSON},
		Temperature:    &temperature,
		MaxTokens:      &maxTokens,
		TopP:           &topP,
		ToolChoice:     llm.ToolChoice{Mode: llm.ToolChoiceRequired},
	})

	got := a.ApplyAgentOptions(agent.RunOptions{})
	if got.Model != model {
		t.Errorf("Model = %v, want the agent model", got.Model)
	}
	if got.Policy != policy {
		t.Errorf("Policy = %v, want the agent policy", got.Policy)
	}
	if got.Approver != approver {
		t.Errorf("Approver = %v, want the agent approver", got.Approver)
	}
	if got.Store != store {
		t.Errorf("Store = %v, want the agent store", got.Store)
	}
	if want := (agent.Budget{MaxSteps: 4, MaxToolCalls: 2, MaxTotalTokens: 100, MaxDuration: time.Minute}); got.Budget != want {
		t.Errorf("Budget = %+v, want %+v", got.Budget, want)
	}
	if want := (agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute}); got.Retry != want {
		t.Errorf("Retry = %+v, want %+v", got.Retry, want)
	}
	if got.ToolTimeout != 30*time.Second {
		t.Errorf("ToolTimeout = %v, want 30s", got.ToolTimeout)
	}
	if got.Thinking == nil || got.Thinking.Level != llm.ThinkingLevelHigh {
		t.Errorf("Thinking = %+v, want high", got.Thinking)
	}
	if got.ResponseFormat == nil || got.ResponseFormat.Type != llm.ResponseFormatJSON {
		t.Errorf("ResponseFormat = %+v, want JSON", got.ResponseFormat)
	}
	if got.Temperature == nil || *got.Temperature != temperature {
		t.Errorf("Temperature = %v, want %v", got.Temperature, temperature)
	}
	if got.MaxTokens == nil || *got.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %v, want %v", got.MaxTokens, maxTokens)
	}
	if got.TopP == nil || *got.TopP != topP {
		t.Errorf("TopP = %v, want %v", got.TopP, topP)
	}
	if got.ToolChoice.Mode != llm.ToolChoiceRequired {
		t.Errorf("ToolChoice = %+v, want required", got.ToolChoice)
	}

	// The returned pointer fields belong to the caller, not the agent.
	*got.Temperature = 0.9
	*got.MaxTokens = 5
	*got.TopP = 0.1
	got.Thinking.Level = llm.ThinkingLevelLow
	got.ResponseFormat.Type = llm.ResponseFormatText

	again := a.ApplyAgentOptions(agent.RunOptions{})
	if again.Temperature == nil || *again.Temperature != temperature {
		t.Errorf("Temperature after mutation = %v, want %v", again.Temperature, temperature)
	}
	if again.MaxTokens == nil || *again.MaxTokens != maxTokens {
		t.Errorf("MaxTokens after mutation = %v, want %v", again.MaxTokens, maxTokens)
	}
	if again.TopP == nil || *again.TopP != topP {
		t.Errorf("TopP after mutation = %v, want %v", again.TopP, topP)
	}
	if again.Thinking == nil || again.Thinking.Level != llm.ThinkingLevelHigh {
		t.Errorf("Thinking after mutation = %+v, want high", again.Thinking)
	}
	if again.ResponseFormat == nil || again.ResponseFormat.Type != llm.ResponseFormatJSON {
		t.Errorf("ResponseFormat after mutation = %+v, want JSON", again.ResponseFormat)
	}
}

func TestAgent_ApplyAgentOptionsRunOverrides(t *testing.T) {
	agentTemperature := 0.4
	agentMaxTokens := 64
	agentTopP := 0.7
	runTemperature := 0.1
	runMaxTokens := 5
	runTopP := 0.2
	runModel := &scriptModel{}
	runPolicy := &stubPolicy{}
	runApprover := &stubApprover{}
	runStore := &recordingStore{}
	a := mustAgent(t, agent.Config{
		Name:           "assistant",
		Model:          &scriptModel{},
		Policy:         &stubPolicy{},
		Approver:       &stubApprover{},
		Store:          &recordingStore{},
		Budget:         agent.Budget{MaxSteps: 4, MaxToolCalls: 2, MaxTotalTokens: 100, MaxDuration: time.Minute},
		Retry:          agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute},
		ToolTimeout:    30 * time.Second,
		Thinking:       &llm.ThinkingConfig{Level: llm.ThinkingLevelHigh},
		ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSON},
		Temperature:    &agentTemperature,
		MaxTokens:      &agentMaxTokens,
		TopP:           &agentTopP,
		ToolChoice:     llm.ToolChoice{Mode: llm.ToolChoiceRequired},
	})

	got := a.ApplyAgentOptions(agent.RunOptions{
		Model:          runModel,
		Policy:         runPolicy,
		Approver:       runApprover,
		Store:          runStore,
		Budget:         agent.Budget{MaxSteps: 8},
		Retry:          agent.RetryPolicy{MaxAttempts: 1},
		ToolTimeout:    -1,
		Thinking:       &llm.ThinkingConfig{Level: llm.ThinkingLevelLow},
		ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatText},
		Temperature:    &runTemperature,
		MaxTokens:      &runMaxTokens,
		TopP:           &runTopP,
		ToolChoice:     llm.ToolChoice{Mode: llm.ToolChoiceNone},
	})

	if got.Model != runModel {
		t.Errorf("Model = %v, want the run model", got.Model)
	}
	if got.Policy != runPolicy {
		t.Errorf("Policy = %v, want the run policy", got.Policy)
	}
	if got.Approver != runApprover {
		t.Errorf("Approver = %v, want the run approver", got.Approver)
	}
	if got.Store != runStore {
		t.Errorf("Store = %v, want the run store", got.Store)
	}
	if want := (agent.Budget{MaxSteps: 8, MaxToolCalls: 2, MaxTotalTokens: 100, MaxDuration: time.Minute}); got.Budget != want {
		t.Errorf("Budget = %+v, want %+v", got.Budget, want)
	}
	if want := (agent.RetryPolicy{MaxAttempts: 1, InitialDelay: time.Second, MaxDelay: time.Minute}); got.Retry != want {
		t.Errorf("Retry = %+v, want %+v", got.Retry, want)
	}
	if got.ToolTimeout != -1 {
		t.Errorf("ToolTimeout = %v, want -1", got.ToolTimeout)
	}
	if got.Thinking == nil || got.Thinking.Level != llm.ThinkingLevelLow {
		t.Errorf("Thinking = %+v, want low", got.Thinking)
	}
	if got.ResponseFormat == nil || got.ResponseFormat.Type != llm.ResponseFormatText {
		t.Errorf("ResponseFormat = %+v, want text", got.ResponseFormat)
	}
	if got.Temperature == nil || *got.Temperature != runTemperature {
		t.Errorf("Temperature = %v, want %v", got.Temperature, runTemperature)
	}
	if got.MaxTokens == nil || *got.MaxTokens != runMaxTokens {
		t.Errorf("MaxTokens = %v, want %v", got.MaxTokens, runMaxTokens)
	}
	if got.TopP == nil || *got.TopP != runTopP {
		t.Errorf("TopP = %v, want %v", got.TopP, runTopP)
	}
	if got.ToolChoice.Mode != llm.ToolChoiceNone {
		t.Errorf("ToolChoice = %+v, want none", got.ToolChoice)
	}
}

func TestAgent_ApplyAgentOptionsNilAgent(t *testing.T) {
	var a *agent.Agent
	got := a.ApplyAgentOptions(agent.RunOptions{ToolTimeout: time.Second})
	if got.ToolTimeout != time.Second {
		t.Errorf("ToolTimeout = %v, want 1s", got.ToolTimeout)
	}
	if got.Model != nil {
		t.Errorf("Model = %v, want nil", got.Model)
	}
}

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

func TestAgent_OptionsAreSnapshotted(t *testing.T) {
	temperature := 0.1
	thinking := &llm.ThinkingConfig{Level: llm.ThinkingLevelLow}
	format := &llm.ResponseFormat{Type: llm.ResponseFormatJSON}
	model := &scriptModel{steps: []step{finalText("ok")}}
	a := mustAgent(t, agent.Config{
		Name:           "assistant",
		Model:          model,
		Thinking:       thinking,
		ResponseFormat: format,
		Temperature:    &temperature,
	})

	temperature = 0.9
	thinking.Level = llm.ThinkingLevelHigh
	format.Type = llm.ResponseFormatText

	if _, err := a.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	request := model.Calls()[0]
	if request.Temperature == nil || *request.Temperature != 0.1 {
		t.Errorf("Temperature = %v, want 0.1", request.Temperature)
	}
	if request.Thinking == nil || request.Thinking.Level != llm.ThinkingLevelLow {
		t.Errorf("Thinking = %+v, want low", request.Thinking)
	}
	if request.ResponseFormat == nil || request.ResponseFormat.Type != llm.ResponseFormatJSON {
		t.Errorf("ResponseFormat = %+v, want JSON", request.ResponseFormat)
	}
}

func TestAgent_RunOverridesDoNotLeak(t *testing.T) {
	temperature := 0.2
	model := &scriptModel{steps: []step{finalText("first"), finalText("second")}}
	a := mustAgent(t, agent.Config{
		Name:        "assistant",
		Model:       model,
		Temperature: &temperature,
	})

	if _, err := a.Generate(context.Background(), "first", agent.WithTemperature(0.9)); err != nil {
		t.Fatalf("Generate first: %v", err)
	}
	if _, err := a.Generate(context.Background(), "second"); err != nil {
		t.Fatalf("Generate second: %v", err)
	}

	calls := model.Calls()
	if calls[0].Temperature == nil || *calls[0].Temperature != 0.9 {
		t.Errorf("first Temperature = %v, want 0.9", calls[0].Temperature)
	}
	if calls[1].Temperature == nil || *calls[1].Temperature != 0.2 {
		t.Errorf("second Temperature = %v, want the agent option 0.2", calls[1].Temperature)
	}
}
