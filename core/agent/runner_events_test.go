package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_ToolEncodeFailureEmitsOneToolEnd(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{CallID: "call-1", Name: "bad", Input: json.RawMessage(`{"value":"x"}`)}),
		finalText("done"),
	}}
	bad := tool.MustNew(tool.Config[echoInput, tool.Result]{
		ID: "bad",
		Execute: func(context.Context, echoInput) (tool.Result, error) {
			return tool.Result{
				Content: []llm.Part{llm.NewTextPart("ok")},
				Data:    make(chan int),
			}, nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "encode", Model: model, Tools: []tool.Tool{bad}})
	sink := &eventRecorder{}

	if _, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Sink: sink}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var toolEnds []agent.ToolEnd
	for _, event := range sink.Events() {
		if typed, ok := event.(agent.ToolEnd); ok {
			toolEnds = append(toolEnds, typed)
		}
	}
	if len(toolEnds) != 1 {
		t.Fatalf("ToolEnd events = %d, want 1", len(toolEnds))
	}
	if !errors.Is(toolEnds[0].Err, tool.ErrOutputEncode) {
		t.Errorf("ToolEnd err = %v, want ErrOutputEncode", toolEnds[0].Err)
	}
}

// afterToolErrorPolicy fails AfterTool to exercise the fatal policy path.
type afterToolErrorPolicy struct {
	err error
}

func (p afterToolErrorPolicy) BeforeModel(context.Context, *agent.ModelRequest) error { return nil }
func (p afterToolErrorPolicy) AfterModel(context.Context, *agent.ModelResult) error   { return nil }
func (p afterToolErrorPolicy) BeforeTool(context.Context, *agent.ToolRequest) (agent.Decision, error) {
	return agent.Allow, nil
}
func (p afterToolErrorPolicy) AfterTool(context.Context, *agent.ToolResult) error {
	return p.err
}

func TestAgent_ToolEventsArePaired(t *testing.T) {
	policyErr := errors.New("policy failed")
	afterErr := errors.New("after tool failed")
	execErr := errors.New("tool failed")
	echo := newEchoTool(t, "echo", nil)
	failing := tool.MustNew(tool.Config[struct{}, string]{
		ID: "failing",
		Execute: func(context.Context, struct{}) (string, error) {
			return "", execErr
		},
	})

	tests := []struct {
		name          string
		policy        agent.Policy
		tools         []tool.Tool
		call          llm.ToolCall
		wantRunErr    error
		wantEndErr    error
		wantEndErrSub string
		wantResult    bool
	}{
		{
			name:       "allow",
			tools:      []tool.Tool{echo},
			call:       llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantResult: true,
		},
		{
			name: "deny",
			policy: agent.Hooks{
				OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
					return agent.Deny, nil
				},
			},
			tools:      []tool.Tool{echo},
			call:       llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantEndErr: agent.ErrToolDenied,
		},
		{
			name:          "tool not found",
			call:          llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantEndErrSub: "not found",
		},
		{
			name: "terminate",
			policy: agent.Hooks{
				OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
					return agent.Terminate, nil
				},
			},
			tools:      []tool.Tool{echo},
			call:       llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantRunErr: agent.ErrTerminated,
			wantEndErr: agent.ErrTerminated,
		},
		{
			name: "before tool error",
			policy: agent.Hooks{
				OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
					return agent.Deny, policyErr
				},
			},
			tools:      []tool.Tool{echo},
			call:       llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantRunErr: policyErr,
			wantEndErr: policyErr,
		},
		{
			name:       "after tool error",
			policy:     afterToolErrorPolicy{err: afterErr},
			tools:      []tool.Tool{echo},
			call:       llm.ToolCall{CallID: "c1", Name: "echo", Input: json.RawMessage(`{"value":"x"}`)},
			wantRunErr: afterErr,
			wantEndErr: afterErr,
			wantResult: true,
		},
		{
			name:       "execute error",
			tools:      []tool.Tool{failing},
			call:       llm.ToolCall{CallID: "c1", Name: "failing", Input: json.RawMessage(`{}`)},
			wantEndErr: execErr,
			wantResult: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{steps: []step{
				toolCallStep(tc.call),
				finalText("done"),
			}}
			a := mustAgent(t, agent.Config{Name: "pairing", Model: model, Tools: tc.tools})
			sink := &eventRecorder{}

			_, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
				Policy: tc.policy,
				Sink:   sink,
			})
			if !errors.Is(err, tc.wantRunErr) {
				t.Fatalf("run err = %v, want %v", err, tc.wantRunErr)
			}

			var starts, ends int
			var end agent.ToolEnd
			for _, event := range sink.Events() {
				switch typed := event.(type) {
				case agent.ToolStart:
					starts++
				case agent.ToolEnd:
					ends++
					end = typed
				}
			}
			if starts != 1 || ends != 1 {
				t.Fatalf("tool events = %d starts / %d ends, want 1/1", starts, ends)
			}
			if tc.wantEndErr != nil && !errors.Is(end.Err, tc.wantEndErr) {
				t.Errorf("ToolEnd err = %v, want %v", end.Err, tc.wantEndErr)
			}
			if tc.wantEndErrSub != "" {
				if end.Err == nil || !strings.Contains(end.Err.Error(), tc.wantEndErrSub) {
					t.Errorf("ToolEnd err = %v, want substring %q", end.Err, tc.wantEndErrSub)
				}
			}
			if tc.wantEndErr == nil && tc.wantEndErrSub == "" && end.Err != nil {
				t.Errorf("ToolEnd err = %v, want nil", end.Err)
			}
			if got := end.Result != nil; got != tc.wantResult {
				t.Errorf("ToolEnd result present = %v, want %v", got, tc.wantResult)
			}
		})
	}
}
