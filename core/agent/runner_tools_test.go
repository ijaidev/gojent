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
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_UniqueToolCallIDs(t *testing.T) {
	tests := []struct {
		name  string
		calls []llm.ToolCall
	}{
		{
			name: "empty ids",
			calls: []llm.ToolCall{
				{Name: "echo", Arguments: `{"value":"one"}`},
				{Name: "echo", Arguments: `{"value":"two"}`},
			},
		},
		{
			name: "duplicate ids",
			calls: []llm.ToolCall{
				{ID: "same", Name: "echo", Arguments: `{"value":"one"}`},
				{ID: "same", Name: "echo", Arguments: `{"value":"two"}`},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{steps: []step{
				toolCallStep(tc.calls...),
				finalText("done"),
			}}
			executed := 0
			echo := tool.MustNew(tool.Config[echoInput, string]{
				ID: "echo",
				Execute: func(_ context.Context, _ echoInput) (string, error) {
					executed++
					return "ok", nil
				},
			})
			a := mustAgent(t, agent.Config{Name: "multi", Model: model, Tools: []tool.Tool{echo}})
			store := &recordingStore{}

			resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if executed != 2 {
				t.Fatalf("executed tools = %d, want 2", executed)
			}
			if len(resp.ToolCalls) != 2 {
				t.Fatalf("response tool calls = %d, want 2", len(resp.ToolCalls))
			}
			if resp.ToolCalls[0].ID == resp.ToolCalls[1].ID {
				t.Fatalf("tool call IDs are not unique: %q", resp.ToolCalls[0].ID)
			}
			if resp.ToolCalls[0].ID == "" || resp.ToolCalls[1].ID == "" {
				t.Fatalf("tool call ID is empty: %+v", resp.ToolCalls)
			}
			for i, call := range resp.ToolCalls {
				if call.Index != i {
					t.Errorf("call %d index = %d, want %d", i, call.Index, i)
				}
			}
			assertNoDanglingToolCalls(t, store.Messages())
		})
	}
}

func TestAgent_ToolPanicIsContained(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "boom", Name: "boom", Arguments: `{}`}),
		finalText("recovered"),
	}}
	boom := tool.MustNew(tool.Config[struct{}, string]{
		ID: "boom",
		Execute: func(_ context.Context, _ struct{}) (string, error) {
			panic("tool exploded")
		},
	})
	a := mustAgent(t, agent.Config{Name: "panic", Model: model, Tools: []tool.Tool{boom}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("Text = %q", resp.Text)
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "panic: tool exploded") {
		t.Errorf("panic was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)

	// The next model request must include the error result, not a crash.
	calls := model.Calls()
	result := calls[1].Messages[2]
	if !result.IsError {
		t.Errorf("replayed tool result is not marked as an error: %+v", result)
	}
}

func TestAgent_ToolTimeoutIsContained(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "slow", Name: "slow", Arguments: `{}`}),
		finalText("gave up"),
	}}
	slow := tool.MustNew(tool.Config[struct{}, string]{
		ID: "slow",
		Execute: func(_ context.Context, _ struct{}) (string, error) {
			<-release
			return "late", nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "timeout", Model: model, Tools: []tool.Tool{slow}})
	store := &recordingStore{}

	started := time.Now()
	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:       store,
		ToolTimeout: 25 * time.Millisecond,
		Session:     &session.Session{ID: "s"},
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("run took %v; a blocking tool was not contained", elapsed)
	}
	if resp.Text != "gave up" {
		t.Errorf("Text = %q", resp.Text)
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "deadline exceeded") {
		t.Errorf("timeout was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}

func TestAgent_ToolResultDropsEmptyTextParts(t *testing.T) {
	empty := tool.MustNew(tool.Config[struct{}, tool.Result]{
		ID: "empty",
		Execute: func(_ context.Context, _ struct{}) (tool.Result, error) {
			return tool.Result{Content: []llm.Part{llm.TextPart("")}}, nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "c1", Name: "empty", Arguments: `{}`}),
		finalText("done"),
	}}
	a := mustAgent(t, agent.Config{Name: "empty-parts", Model: model, Tools: []tool.Tool{empty}})

	if _, err := a.Generate(context.Background(), "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	for _, message := range calls[1].Messages {
		for _, part := range message.Content {
			if part.Type == llm.PartTypeText && part.Text == "" {
				t.Fatalf("empty text part reached the provider: %+v", calls[1].Messages)
			}
		}
	}
	last := calls[1].Messages[len(calls[1].Messages)-1]
	if last.Role != llm.RoleTool || last.Text() != "{}" {
		t.Errorf("tool message = %+v, want a single {} text part", last)
	}
}

func TestAgent_ToolCallIDCollisionWithGeneratedID(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call_1_0", Name: "echo", Arguments: `{"value":"one"}`}),
		toolCallStep(llm.ToolCall{ID: "call_1_0", Name: "echo", Arguments: `{"value":"two"}`}),
		finalText("done"),
	}}
	echo := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(context.Context, echoInput) (string, error) {
			return "ok", nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "collision", Model: model, Tools: []tool.Tool{echo}})

	resp, err := a.Generate(context.Background(), "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := make(map[string]bool, len(resp.ToolCalls))
	for _, call := range resp.ToolCalls {
		if seen[call.ID] {
			t.Fatalf("duplicate tool call ID %q in %+v", call.ID, resp.ToolCalls)
		}
		seen[call.ID] = true
	}
}

func TestAgent_ToolBudgetPersistsAllResults(t *testing.T) {
	executed := 0
	echo := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(_ context.Context, _ echoInput) (string, error) {
			executed++
			return "ok", nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(
			llm.ToolCall{ID: "a", Name: "echo", Arguments: `{"value":"a"}`},
			llm.ToolCall{ID: "b", Name: "echo", Arguments: `{"value":"b"}`},
		),
		finalText("never reached"),
	}}
	a := mustAgent(t, agent.Config{Name: "budget", Model: model, Tools: []tool.Tool{echo}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Budget:  agent.Budget{MaxToolCalls: 1},
		Session: &session.Session{ID: "s"},
	})
	if !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if resp == nil {
		t.Fatal("expected a partial response")
	}
	if executed != 1 {
		t.Errorf("executed tools = %d, want 1", executed)
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d, want user, assistant, and two results", len(messages))
	}
	assertNoDanglingToolCalls(t, messages)
	if messages[2].IsError {
		t.Errorf("first result should have executed: %+v", messages[2])
	}
	if !messages[3].IsError {
		t.Errorf("over-budget result is not marked as an error: %+v", messages[3])
	}
}

func TestAgent_ValidatesToolArguments(t *testing.T) {
	executed := false
	strict := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(_ context.Context, _ echoInput) (string, error) {
			executed = true
			return "ok", nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}),
		finalText("done"),
	}}
	a := mustAgent(t, agent.Config{Name: "validation", Model: model, Tools: []tool.Tool{strict}})
	store := &recordingStore{}

	if _, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed {
		t.Fatal("tool handler ran with invalid arguments")
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "missing required property") {
		t.Errorf("validation failure was not reported to the model: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}
