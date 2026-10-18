package agent_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

// newEchoTool returns a tool that echoes its raw input, prefixed with "echoed:".
func newEchoTool() tool.Tool {
	return tool.MustNew(tool.Config[string, string]{
		ID:          "echo",
		Description: "Echo the raw input back to the model",
		Execute: func(_ context.Context, in string) (string, error) {
			return "echoed:" + in, nil
		},
	})
}

// toolCallingMock returns a mock that requests the given tool once and then
// responds with finalText.
func toolCallingMock(toolName string, finalText string) *llm.MockLLM {
	callCount := 0
	return llm.NewMockLLM().ApplyOptions(
		llm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls: []llm.ToolCall{
						{ID: "call-1", Name: toolName, Arguments: `"ping"`},
					},
					Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			}
			return &llm.GenerateResponse{
				Text:         finalText,
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			}, nil
		}),
	)
}

func TestHooks_GenerateLifecycle(t *testing.T) {
	mock := toolCallingMock("echo", "final answer")

	var log []string
	var modelEvents []*agent.ModelEvent
	var toolEvents []*agent.ToolEvent

	hooks := agent.Hooks{
		BeforeModel: func(_ context.Context, ev *agent.ModelEvent) error {
			log = append(log, fmt.Sprintf("before_model:%d", ev.Step))
			if ev.Request == nil {
				t.Error("BeforeModel: expected a non-nil request")
			}
			return nil
		},
		AfterModel: func(_ context.Context, ev *agent.ModelEvent) {
			log = append(log, fmt.Sprintf("after_model:%d", ev.Step))
			modelEvents = append(modelEvents, ev)
		},
		BeforeTool: func(_ context.Context, ev *agent.ToolEvent) error {
			log = append(log, fmt.Sprintf("before_tool:%d", ev.Step))
			return nil
		},
		AfterTool: func(_ context.Context, ev *agent.ToolEvent) {
			log = append(log, fmt.Sprintf("after_tool:%d", ev.Step))
			toolEvents = append(toolEvents, ev)
		},
	}

	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: mock,
		Tools: []tool.Tool{newEchoTool()},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	res, err := a.Generate(context.Background(), "do it", agent.WithHooks(hooks))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Text != "final answer" {
		t.Errorf("expected final answer text, got %q", res.Text)
	}

	wantLog := []string{
		"before_model:0",
		"after_model:0",
		"before_tool:0",
		"after_tool:0",
		"before_model:1",
		"after_model:1",
	}
	if !slices.Equal(log, wantLog) {
		t.Errorf("unexpected hook order:\n got: %v\nwant: %v", log, wantLog)
	}

	if len(modelEvents) != 2 {
		t.Fatalf("expected 2 AfterModel events, got %d", len(modelEvents))
	}
	if modelEvents[0].Step != 0 || modelEvents[0].Err != nil {
		t.Errorf("unexpected first AfterModel event: %+v", modelEvents[0])
	}
	if len(modelEvents[0].Response.ToolCalls) != 1 || modelEvents[0].Response.ToolCalls[0].Name != "echo" {
		t.Errorf("expected first step response with echo tool call, got %+v", modelEvents[0].Response)
	}
	if modelEvents[1].Step != 1 || modelEvents[1].Response.Text != "final answer" {
		t.Errorf("unexpected second AfterModel event: %+v", modelEvents[1])
	}
	if modelEvents[1].Request == nil {
		t.Error("expected second AfterModel event to carry the request")
	}

	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 AfterTool event, got %d", len(toolEvents))
	}
	ev := toolEvents[0]
	if ev.Step != 0 || ev.Skipped || ev.Err != nil {
		t.Errorf("unexpected tool event fields: %+v", ev)
	}
	if !strings.Contains(ev.Output, "echoed") {
		t.Errorf("expected echoed tool output, got %q", ev.Output)
	}
	if ev.Call.Name != "echo" {
		t.Errorf("expected tool call name echo, got %q", ev.Call.Name)
	}
}

func TestHooks_BeforeModelMutatesRequest(t *testing.T) {
	mock := llm.NewMockLLM("ok")

	a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	temp := 0.25
	hooks := agent.Hooks{
		BeforeModel: func(_ context.Context, ev *agent.ModelEvent) error {
			if ev.Step == 0 {
				ev.Request.Temperature = &temp
				ev.Request.Messages = append(ev.Request.Messages, llm.SystemMessage("injected by hook"))
			}
			return nil
		},
	}

	if _, err := a.Generate(context.Background(), "hi", agent.WithHooks(hooks)); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	last := mock.LastCall()
	if last == nil {
		t.Fatal("expected mock to record a call")
	}
	if last.Temperature == nil || *last.Temperature != temp {
		t.Errorf("expected mutated temperature %v to reach the model, got %v", temp, last.Temperature)
	}
	found := false
	for _, m := range last.Messages {
		if m.Content == "injected by hook" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected injected message to reach the model, got %+v", last.Messages)
	}
}

func TestHooks_BeforeModelErrorAborts(t *testing.T) {
	mock := llm.NewMockLLM("ok")

	a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	denied := errors.New("model call denied by guardrail")
	afterModelCalls := 0
	hooks := agent.Hooks{
		BeforeModel: func(_ context.Context, _ *agent.ModelEvent) error { return denied },
		AfterModel:  func(_ context.Context, _ *agent.ModelEvent) { afterModelCalls++ },
	}

	_, err = a.Generate(context.Background(), "hi", agent.WithHooks(hooks))
	if !errors.Is(err, denied) {
		t.Fatalf("expected denied error, got %v", err)
	}
	if mock.CallCount() != 0 {
		t.Errorf("expected no model calls, got %d", mock.CallCount())
	}
	if afterModelCalls != 0 {
		t.Errorf("expected no AfterModel events when the model was never called, got %d", afterModelCalls)
	}
}

func TestHooks_BeforeToolSkip(t *testing.T) {
	executed := false
	riskyTool := tool.MustNew(tool.Config[string, string]{
		ID:          "risky",
		Description: "A risky action",
		Execute: func(_ context.Context, _ string) (string, error) {
			executed = true
			return "boom", nil
		},
	})

	mock := toolCallingMock("risky", "recovered")

	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: mock,
		Tools: []tool.Tool{riskyTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	var captured *agent.ToolEvent
	hooks := agent.Hooks{
		BeforeTool: func(_ context.Context, _ *agent.ToolEvent) error {
			return errors.New("denied by policy")
		},
		AfterTool: func(_ context.Context, ev *agent.ToolEvent) {
			captured = ev
		},
	}

	res, err := a.Generate(context.Background(), "do risky", agent.WithHooks(hooks))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if executed {
		t.Error("expected the risky tool to be skipped, but it executed")
	}
	if captured == nil {
		t.Fatal("expected an AfterTool event for the skipped tool")
	}
	if !captured.Skipped {
		t.Error("expected the tool event to be marked skipped")
	}
	if captured.Err == nil || !strings.Contains(captured.Err.Error(), "denied by policy") {
		t.Errorf("expected the denial error on the event, got %v", captured.Err)
	}
	if !strings.Contains(captured.Output, "denied by policy") {
		t.Errorf("expected the denial in the tool output, got %q", captured.Output)
	}

	// The denial must reach the model as the tool result so it can adapt.
	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(calls))
	}
	secondCall := calls[1]
	last := secondCall.Messages[len(secondCall.Messages)-1]
	if last.Role != llm.RoleTool || !strings.Contains(last.Content, "denied by policy") {
		t.Errorf("expected denial tool message sent to model, got %+v", last)
	}
	if res.Text != "recovered" {
		t.Errorf("expected the model to continue after the skip, got %q", res.Text)
	}
}

func TestHooks_AfterToolError(t *testing.T) {
	failTool := tool.MustNew(tool.Config[string, string]{
		ID:          "fail",
		Description: "Always fails",
		Execute: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("kaboom")
		},
	})

	mock := toolCallingMock("fail", "handled the failure")

	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: mock,
		Tools: []tool.Tool{failTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	var captured *agent.ToolEvent
	hooks := agent.Hooks{
		AfterTool: func(_ context.Context, ev *agent.ToolEvent) {
			captured = ev
		},
	}

	res, err := a.Generate(context.Background(), "run failing tool", agent.WithHooks(hooks))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if captured == nil {
		t.Fatal("expected an AfterTool event")
	}
	if captured.Skipped {
		t.Error("expected the tool event to not be marked skipped")
	}
	if captured.Err == nil || captured.Err.Error() != "kaboom" {
		t.Errorf("expected kaboom error on the event, got %v", captured.Err)
	}
	if !strings.Contains(captured.Output, "kaboom") {
		t.Errorf("expected the error in the tool output, got %q", captured.Output)
	}
	if res.Text != "handled the failure" {
		t.Errorf("expected the run to continue after the tool error, got %q", res.Text)
	}
}

func TestHooks_UnknownToolReportsError(t *testing.T) {
	callCount := 0
	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "ghost", Arguments: `{}`}},
				}, nil
			}
			return &llm.GenerateResponse{Text: "ok", FinishReason: llm.FinishReasonStop}, nil
		}),
	)

	a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	var captured *agent.ToolEvent
	hooks := agent.Hooks{
		AfterTool: func(_ context.Context, ev *agent.ToolEvent) {
			captured = ev
		},
	}

	if _, err := a.Generate(context.Background(), "call ghost", agent.WithHooks(hooks)); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if captured == nil {
		t.Fatal("expected an AfterTool event for the unknown tool")
	}
	if captured.Err == nil || !strings.Contains(captured.Err.Error(), "ghost") {
		t.Errorf("expected an unknown tool error, got %v", captured.Err)
	}
	if !strings.Contains(captured.Output, "not found") {
		t.Errorf("expected a not found output, got %q", captured.Output)
	}
}

// failingStorage overrides persistence operations to always fail.
type failingStorage struct {
	storage.Storage
	saveErr error
	listErr error
}

func (f *failingStorage) SaveMessages(_ context.Context, _ []*storage.Message) ([]*storage.Message, error) {
	return nil, f.saveErr
}

func (f *failingStorage) ListMessages(_ context.Context, _ storage.MessageFilter) ([]*storage.Message, error) {
	return nil, f.listErr
}

func TestHooks_StorageError(t *testing.T) {
	saveErr := errors.New("disk full")
	listErr := errors.New("connection reset")
	failing := &failingStorage{
		Storage: storage.NewMockStorage(),
		saveErr: saveErr,
		listErr: listErr,
	}

	a, err := agent.New(agent.Config{ID: "hooked-agent", Model: llm.NewMockLLM("hello")})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	var ops []string
	var errs []error
	hooks := agent.Hooks{
		OnStorageError: func(_ context.Context, op string, err error) {
			ops = append(ops, op)
			errs = append(errs, err)
		},
	}

	res, err := a.Generate(context.Background(), "hi",
		agent.WithStorage(failing),
		agent.WithThreadID("thread-hooks"),
		agent.WithHooks(hooks),
	)
	if err != nil {
		t.Fatalf("storage failures must not abort generation, got %v", err)
	}
	if res.Text != "hello" {
		t.Errorf("expected the run to complete despite storage failures, got %q", res.Text)
	}

	wantOps := []string{"list_messages", "save_messages", "save_messages"}
	if !slices.Equal(ops, wantOps) {
		t.Fatalf("unexpected storage error ops:\n got: %v\nwant: %v", ops, wantOps)
	}
	if !errors.Is(errs[0], listErr) {
		t.Errorf("expected list error, got %v", errs[0])
	}
	for _, err := range errs[1:] {
		if !errors.Is(err, saveErr) {
			t.Errorf("expected save error, got %v", err)
		}
	}
}

func TestHooks_StreamLifecycle(t *testing.T) {
	callCount := 0
	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomStreamHandler(func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			callCount++
			if callCount == 1 {
				return llm.NewSliceStream(ctx, []*llm.StreamChunk{
					{
						Delta:        "checking",
						ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: `"ping"`}},
						FinishReason: llm.FinishReasonTool,
					},
				}), nil
			}
			return llm.NewSliceStream(ctx, []*llm.StreamChunk{
				{Delta: "done", FinishReason: llm.FinishReasonStop},
			}), nil
		}),
	)

	var log []string
	var modelEvents []*agent.ModelEvent
	hooks := agent.Hooks{
		BeforeModel: func(_ context.Context, ev *agent.ModelEvent) error {
			log = append(log, fmt.Sprintf("before_model:%d", ev.Step))
			return nil
		},
		AfterModel: func(_ context.Context, ev *agent.ModelEvent) {
			log = append(log, fmt.Sprintf("after_model:%d", ev.Step))
			modelEvents = append(modelEvents, ev)
		},
		BeforeTool: func(_ context.Context, ev *agent.ToolEvent) error {
			log = append(log, fmt.Sprintf("before_tool:%d", ev.Step))
			return nil
		},
		AfterTool: func(_ context.Context, ev *agent.ToolEvent) {
			log = append(log, fmt.Sprintf("after_tool:%d:%s", ev.Step, ev.Output))
		},
	}

	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: mock,
		Tools: []tool.Tool{newEchoTool()},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := a.Stream(context.Background(), "go", agent.WithHooks(hooks))
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if res.Text != "done" {
		t.Errorf("expected streamed final text 'done', got %q", res.Text)
	}

	wantLog := []string{
		"before_model:0",
		"after_model:0",
		"before_tool:0",
		`after_tool:0:echoed:"ping"`,
		"before_model:1",
		"after_model:1",
	}
	if !slices.Equal(log, wantLog) {
		t.Errorf("unexpected stream hook order:\n got: %v\nwant: %v", log, wantLog)
	}

	if len(modelEvents) != 2 {
		t.Fatalf("expected 2 AfterModel events, got %d", len(modelEvents))
	}
	if modelEvents[0].Response == nil || len(modelEvents[0].Response.ToolCalls) != 1 {
		t.Errorf("expected first streamed step to expose its tool calls, got %+v", modelEvents[0].Response)
	}
	if modelEvents[0].Response.Text != "checking" {
		t.Errorf("expected first streamed step text, got %q", modelEvents[0].Response.Text)
	}
	if modelEvents[1].Response == nil || modelEvents[1].Response.Text != "done" {
		t.Errorf("expected second streamed step final text, got %+v", modelEvents[1].Response)
	}
}

// errorStream is a stream whose Recv fails immediately.
type errorStream struct {
	err error
}

func (e *errorStream) Recv() (*llm.StreamChunk, error) { return nil, e.err }
func (e *errorStream) Close() error                    { return nil }

func TestHooks_StreamError(t *testing.T) {
	streamErr := errors.New("stream exploded")

	t.Run("recv failure", func(t *testing.T) {
		mock := llm.NewMockLLM().ApplyOptions(
			llm.WithCustomStreamHandler(func(_ context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
				return &errorStream{err: streamErr}, nil
			}),
		)

		a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		var events []*agent.ModelEvent
		hooks := agent.Hooks{
			AfterModel: func(_ context.Context, ev *agent.ModelEvent) {
				events = append(events, ev)
			},
		}

		stream, err := a.Stream(context.Background(), "hi", agent.WithHooks(hooks))
		if err != nil {
			t.Fatalf("Stream failed: %v", err)
		}
		if _, err := stream.Recv(); !errors.Is(err, streamErr) {
			t.Fatalf("expected stream error, got %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 AfterModel event, got %d", len(events))
		}
		if !errors.Is(events[0].Err, streamErr) {
			t.Errorf("expected the stream error on the event, got %v", events[0].Err)
		}
		if events[0].Step != 0 {
			t.Errorf("expected step 0 event, got %d", events[0].Step)
		}
	})

	t.Run("stream creation failure", func(t *testing.T) {
		mock := llm.NewMockLLM().ApplyOptions(
			llm.WithCustomStreamHandler(func(_ context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
				return nil, streamErr
			}),
		)

		a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		var events []*agent.ModelEvent
		hooks := agent.Hooks{
			AfterModel: func(_ context.Context, ev *agent.ModelEvent) {
				events = append(events, ev)
			},
		}

		if _, err := a.Stream(context.Background(), "hi", agent.WithHooks(hooks)); !errors.Is(err, streamErr) {
			t.Fatalf("expected stream creation error, got %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 AfterModel event, got %d", len(events))
		}
		if !errors.Is(events[0].Err, streamErr) {
			t.Errorf("expected the stream error on the event, got %v", events[0].Err)
		}
		if events[0].Response != nil {
			t.Errorf("expected nil response on creation failure, got %+v", events[0].Response)
		}
	})
}

func TestHooks_PerCallMergeAndOverride(t *testing.T) {
	mock := toolCallingMock("echo", "done")

	agentBeforeModel := 0
	agentAfterModel := 0
	agentAfterTool := 0

	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: mock,
		Tools: []tool.Tool{newEchoTool()},
		Hooks: agent.Hooks{
			BeforeModel: func(_ context.Context, _ *agent.ModelEvent) error {
				agentBeforeModel++
				return nil
			},
			AfterModel: func(_ context.Context, _ *agent.ModelEvent) {
				agentAfterModel++
			},
			AfterTool: func(_ context.Context, _ *agent.ToolEvent) {
				agentAfterTool++
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	callAfterModel := 0
	callBeforeTool := 0

	perCall := agent.Hooks{
		AfterModel: func(_ context.Context, _ *agent.ModelEvent) {
			callAfterModel++
		},
		BeforeTool: func(_ context.Context, _ *agent.ToolEvent) error {
			callBeforeTool++
			return nil
		},
	}

	if _, err := a.Generate(context.Background(), "go", agent.WithHooks(perCall)); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// The per-call AfterModel overrides the agent-level one...
	if callAfterModel != 2 {
		t.Errorf("expected per-call AfterModel on both steps, got %d", callAfterModel)
	}
	if agentAfterModel != 0 {
		t.Errorf("expected agent-level AfterModel to be overridden, got %d calls", agentAfterModel)
	}
	// ...while uncovered agent-level hooks still fire.
	if agentBeforeModel != 2 {
		t.Errorf("expected agent-level BeforeModel on both steps, got %d", agentBeforeModel)
	}
	if agentAfterTool != 1 {
		t.Errorf("expected agent-level AfterTool to still fire, got %d", agentAfterTool)
	}
	if callBeforeTool != 1 {
		t.Errorf("expected per-call BeforeTool to fire, got %d", callBeforeTool)
	}
}

func TestHooks_RepeatedWithHooksLayers(t *testing.T) {
	mock := llm.NewMockLLM("ok")

	a, err := agent.New(agent.Config{ID: "hooked-agent", Model: mock})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	first := 0
	second := 0
	firstBefore := 0

	_, err = a.Generate(context.Background(), "hi",
		agent.WithHooks(agent.Hooks{
			BeforeModel: func(_ context.Context, _ *agent.ModelEvent) error {
				firstBefore++
				return nil
			},
			AfterModel: func(_ context.Context, _ *agent.ModelEvent) {
				first++
			},
		}),
		agent.WithHooks(agent.Hooks{
			AfterModel: func(_ context.Context, _ *agent.ModelEvent) {
				second++
			},
		}),
	)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if second != 1 {
		t.Errorf("expected second WithHooks AfterModel to win, got %d", second)
	}
	if first != 0 {
		t.Errorf("expected first AfterModel to be overridden, got %d", first)
	}
	if firstBefore != 1 {
		t.Errorf("expected first BeforeModel to be preserved by layering, got %d", firstBefore)
	}
}

func TestHooks_AgentAccessors(t *testing.T) {
	a, err := agent.New(agent.Config{
		ID:    "hooked-agent",
		Model: llm.NewMockLLM("ok"),
		Hooks: agent.Hooks{
			AfterModel: func(_ context.Context, _ *agent.ModelEvent) {},
		},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	if a.Hooks().AfterModel == nil {
		t.Error("expected config hooks to be stored on the agent")
	}

	calls := 0
	a.SetHooks(agent.Hooks{
		BeforeModel: func(_ context.Context, _ *agent.ModelEvent) error {
			calls++
			return nil
		},
	})

	if a.Hooks().BeforeModel == nil {
		t.Fatal("expected SetHooks to replace hooks")
	}
	if a.Hooks().AfterModel != nil {
		t.Error("expected SetHooks to replace the full hook set")
	}

	if _, err := a.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected updated hooks to run, got %d calls", calls)
	}
}
