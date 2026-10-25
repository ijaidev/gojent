package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_PolicyDeniesAndApproves(t *testing.T) {
	tests := []struct {
		name          string
		decision      agent.Decision
		approver      agent.Approver
		wantExecuted  bool
		terminalError error
	}{
		{name: "allow", decision: agent.Allow, wantExecuted: true},
		{name: "deny", decision: agent.Deny, wantExecuted: false},
		{name: "ask without approver", decision: agent.Ask, wantExecuted: false},
		{
			name:         "ask approved",
			decision:     agent.Ask,
			approver:     agent.ApproverFunc(func(context.Context, agent.ToolRequest) (bool, error) { return true, nil }),
			wantExecuted: true,
		},
		{
			name:         "ask rejected",
			decision:     agent.Ask,
			approver:     agent.ApproverFunc(func(context.Context, agent.ToolRequest) (bool, error) { return false, nil }),
			wantExecuted: false,
		},
		{name: "terminate", decision: agent.Terminate, wantExecuted: false, terminalError: agent.ErrTerminated},
		{name: "unknown fails closed", decision: agent.DecisionUnknown, wantExecuted: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executed := false
			risky := tool.MustNew(tool.Config[struct{}, string]{
				ID: "risky",
				Execute: func(_ context.Context, _ struct{}) (string, error) {
					executed = true
					return "acted", nil
				},
			})
			model := &scriptModel{steps: []step{
				toolCallStep(llm.ToolCall{ID: "r1", Name: "risky", Arguments: `{}`}),
				finalText("done"),
			}}
			a := mustAgent(t, agent.Config{Name: "policy", Model: model, Tools: []tool.Tool{risky}})
			store := &recordingStore{}
			policy := agent.Hooks{
				OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
					return tc.decision, nil
				},
			}

			resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
				Store:    store,
				Policy:   policy,
				Approver: tc.approver,
				Session:  &session.Session{ID: "s"},
			})

			if tc.terminalError != nil {
				if !errors.Is(err, tc.terminalError) {
					t.Fatalf("err = %v, want %v", err, tc.terminalError)
				}
				if resp != nil {
					t.Errorf("expected no response on termination")
				}
				// The assistant tool call must not be persisted without results.
				if len(store.Messages()) != 1 {
					t.Errorf("terminated run stored %d messages, want only the user input", len(store.Messages()))
				}
				for _, msg := range store.Messages() {
					if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
						t.Fatalf("terminated run persisted an unanswered tool call")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if executed != tc.wantExecuted {
				t.Errorf("executed = %v, want %v", executed, tc.wantExecuted)
			}
			if tc.wantExecuted {
				if resp.Text != "done" {
					t.Errorf("Text = %q", resp.Text)
				}
				assertNoDanglingToolCalls(t, store.Messages())
			} else {
				messages := store.Messages()
				if len(messages) != 4 {
					t.Fatalf("stored messages = %d", len(messages))
				}
				if !messages[2].IsError {
					t.Errorf("denied call was not reported as an error: %+v", messages[2])
				}
				assertNoDanglingToolCalls(t, messages)
			}
		})
	}
}

func TestAgent_PolicyCanReplaceModelRequest(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("done")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	policy := agent.Hooks{
		OnBeforeModel: func(_ context.Context, req *agent.ModelRequest) error {
			req.Request = &llm.Request{Messages: []llm.Message{llm.UserMessage("replacement")}}
			return nil
		},
	}

	if _, err := a.Generate(context.Background(), "original", agent.WithPolicy(policy)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	messages := model.Calls()[0].Messages
	if len(messages) != 1 || messages[0].Text() != "replacement" {
		t.Fatalf("model request messages = %+v, want the replacement", messages)
	}
}

func TestAgent_PolicyCanReplaceModelResponse(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("original")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	policy := agent.Hooks{
		OnAfterModel: func(_ context.Context, result *agent.ModelResult) {
			result.Response = &llm.Response{
				Message:      llm.AssistantMessage("replaced"),
				FinishReason: llm.FinishReasonStop,
			}
		},
	}

	resp, err := a.Generate(context.Background(), "go", agent.WithPolicy(policy))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Text != "replaced" {
		t.Errorf("Text = %q, want replaced", resp.Text)
	}
}

func TestAgent_PolicyMutationDoesNotAlterTranscript(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("done")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	store := &recordingStore{}
	policy := agent.Hooks{
		OnBeforeModel: func(_ context.Context, req *agent.ModelRequest) error {
			for i := range req.Request.Messages {
				if req.Request.Messages[i].Role != llm.RoleUser {
					continue
				}
				req.Request.Messages[i].Content[0].Text = "redacted"
			}
			return nil
		},
	}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Policy:  policy,
		Session: &session.Session{ID: "s"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, message := range resp.Messages {
		if message.Role == llm.RoleUser && message.Text() != "go" {
			t.Errorf("response user message = %q, want the original content", message.Text())
		}
	}
	for _, message := range store.Messages() {
		if message.Role == llm.RoleUser && message.Text() != "go" {
			t.Errorf("stored user message = %q, want the original content", message.Text())
		}
	}
}
