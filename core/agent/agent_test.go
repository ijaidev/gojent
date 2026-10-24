package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/tool"
)

// bareTool lets tests construct specs that a validated constructor would
// normally reject, such as an empty name.
type bareTool struct {
	name string
}

var _ tool.Tool = bareTool{}

func (t bareTool) Spec() tool.Spec {
	return tool.Spec{Name: t.name}
}

func (t bareTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestNew_RejectsInvalidTools(t *testing.T) {
	model := &scriptModel{}

	tests := []struct {
		name    string
		tools   []tool.Tool
		wantErr error
	}{
		{
			name:    "nil tool",
			tools:   []tool.Tool{nil},
			wantErr: agent.ErrToolNil,
		},
		{
			name:    "empty name",
			tools:   []tool.Tool{bareTool{}},
			wantErr: agent.ErrToolNameRequired,
		},
		{
			name:    "duplicate name",
			tools:   []tool.Tool{bareTool{name: "echo"}, bareTool{name: "echo"}},
			wantErr: agent.ErrDuplicateTool,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agent.New(agent.Config{
				Name:  "test",
				Model: model,
				Tools: tc.tools,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("agent.New err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestGenerateWith_RejectsInvalidTools(t *testing.T) {
	tests := []struct {
		name    string
		tools   []tool.Tool
		wantErr error
	}{
		{
			name:    "nil tool",
			tools:   []tool.Tool{nil},
			wantErr: agent.ErrToolNil,
		},
		{
			name:    "empty name",
			tools:   []tool.Tool{bareTool{}},
			wantErr: agent.ErrToolNameRequired,
		},
		{
			name:    "duplicate name",
			tools:   []tool.Tool{bareTool{name: "echo"}, bareTool{name: "echo"}},
			wantErr: agent.ErrDuplicateTool,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{steps: []step{finalText("unused")}}
			a := mustAgent(t, agent.Config{Name: "test", Model: model})

			_, err := a.Generate(context.Background(), "go", agent.WithTools(tc.tools...))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Generate err = %v, want %v", err, tc.wantErr)
			}
			if model.CallCount() != 0 {
				t.Errorf("model calls = %d, want 0", model.CallCount())
			}
		})
	}
}

func TestGenerateWith_RunToolReplacesAgentTool(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("ok")}}
	echo := newEchoTool(t, "echo", nil)
	replacement := bareTool{name: "echo"}
	a := mustAgent(t, agent.Config{Name: "test", Model: model, Tools: []tool.Tool{echo}})

	if _, err := a.Generate(context.Background(), "go", agent.WithTools(replacement)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	definitions := model.Calls()[0].Tools
	if len(definitions) != 1 || definitions[0].Name != "echo" {
		t.Fatalf("tool definitions = %+v, want one echo", definitions)
	}
}
