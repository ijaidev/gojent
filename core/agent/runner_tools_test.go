package agent_test

import (
	"context"
	"encoding/json"
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
				{Name: "echo", Input: json.RawMessage(`{"value":"one"}`)},
				{Name: "echo", Input: json.RawMessage(`{"value":"two"}`)},
			},
		},
		{
			name: "duplicate ids",
			calls: []llm.ToolCall{
				{CallID: "same", Name: "echo", Input: json.RawMessage(`{"value":"one"}`)},
				{CallID: "same", Name: "echo", Input: json.RawMessage(`{"value":"two"}`)},
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
			if resp.ToolCalls[0].CallID == resp.ToolCalls[1].CallID {
				t.Fatalf("tool call IDs are not unique: %q", resp.ToolCalls[0].CallID)
			}
			if resp.ToolCalls[0].CallID == "" || resp.ToolCalls[1].CallID == "" {
				t.Fatalf("tool call ID is empty: %+v", resp.ToolCalls)
			}
			assertNoDanglingToolCalls(t, store.Messages())
		})
	}
}

func TestAgent_ToolPanicIsContained(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{CallID: "boom", Name: "boom", Input: json.RawMessage(`{}`)}),
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
	if toolResultStatus(messages[2]) != llm.ToolStatusError || !strings.Contains(toolResultText(messages[2]), "panic: tool exploded") {
		t.Errorf("panic was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)

	// The next model request must include the error result, not a crash.
	calls := model.Calls()
	result := calls[1].Messages[2]
	if toolResultStatus(result) != llm.ToolStatusError {
		t.Errorf("replayed tool result is not marked as an error: %+v", result)
	}
}

func TestAgent_ToolTimeoutIsContained(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{CallID: "slow", Name: "slow", Input: json.RawMessage(`{}`)}),
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
	if toolResultStatus(messages[2]) != llm.ToolStatusError || !strings.Contains(toolResultText(messages[2]), "deadline exceeded") {
		t.Errorf("timeout was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}

func TestAgent_ToolResultDropsEmptyTextParts(t *testing.T) {
	empty := tool.MustNew(tool.Config[struct{}, tool.Result]{
		ID: "empty",
		Execute: func(_ context.Context, _ struct{}) (tool.Result, error) {
			return tool.Result{Content: []llm.Part{llm.NewTextPart("")}}, nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{CallID: "c1", Name: "empty", Input: json.RawMessage(`{}`)}),
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
			if text, ok := part.(*llm.TextPart); ok && text.Text == "" {
				t.Fatalf("empty text part reached the provider: %+v", calls[1].Messages)
			}
		}
	}
	last := calls[1].Messages[len(calls[1].Messages)-1]
	res := last.ToolResults()
	if last.Role != llm.RoleUser || len(res) != 1 || res[0].Text() != "{}" {
		t.Errorf("tool message = %+v, want a single {} result", last)
	}
}

func TestAgent_ToolCallIDCollisionWithGeneratedID(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{CallID: "call_1_0", Name: "echo", Input: json.RawMessage(`{"value":"one"}`)}),
		toolCallStep(llm.ToolCall{CallID: "call_1_0", Name: "echo", Input: json.RawMessage(`{"value":"two"}`)}),
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
		if seen[call.CallID] {
			t.Fatalf("duplicate tool call ID %q in %+v", call.CallID, resp.ToolCalls)
		}
		seen[call.CallID] = true
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
			llm.ToolCall{CallID: "a", Name: "echo", Input: json.RawMessage(`{"value":"a"}`)},
			llm.ToolCall{CallID: "b", Name: "echo", Input: json.RawMessage(`{"value":"b"}`)},
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
	if len(messages) != 3 {
		t.Fatalf("stored messages = %d, want user, assistant, and one grouped result message", len(messages))
	}
	assertNoDanglingToolCalls(t, messages)
	results := messages[2].ToolResults()
	if len(results) != 2 {
		t.Fatalf("grouped results = %d, want 2", len(results))
	}
	if results[0].Status != llm.ToolStatusCompleted {
		t.Errorf("first result should have executed: %+v", results[0])
	}
	if results[1].Status != llm.ToolStatusError {
		t.Errorf("over-budget result is not marked as an error: %+v", results[1])
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
		toolCallStep(llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}),
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
	if toolResultStatus(messages[2]) != llm.ToolStatusError || !strings.Contains(toolResultText(messages[2]), "missing required property") {
		t.Errorf("validation failure was not reported to the model: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}
