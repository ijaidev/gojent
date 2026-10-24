package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_AsToolDelegatesToSubagent(t *testing.T) {
	subModel := &scriptModel{steps: []step{finalText("42")}}
	sub := mustAgent(t, agent.Config{
		Name:        "math helper",
		Description: "Solves arithmetic questions",
		Model:       subModel,
	})

	subTool := sub.AsTool()
	spec := subTool.Spec()
	if spec.Name != "math_helper" {
		t.Errorf("tool name = %q, want math_helper", spec.Name)
	}
	if spec.Description != "Solves arithmetic questions" {
		t.Errorf("tool description = %q", spec.Description)
	}

	parentModel := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "sub1", Name: "math_helper", Arguments: `{"prompt":"what is 6*7?"}`}),
		finalText("The answer is 42"),
	}}
	parent := mustAgent(t, agent.Config{Name: "parent", Model: parentModel, Tools: []tool.Tool{subTool}})
	store := &recordingStore{}

	resp, err := parent.GenerateWith(context.Background(), "ask the helper", agent.RunOptions{
		Store:   store,
		Session: &session.Session{ID: "parent"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "The answer is 42" {
		t.Errorf("Text = %q", resp.Text)
	}

	subCalls := subModel.Calls()
	if len(subCalls) != 1 {
		t.Fatalf("subagent calls = %d, want 1", len(subCalls))
	}
	if got := subCalls[0].Messages[0].Text(); got != "what is 6*7?" {
		t.Errorf("subagent prompt = %q", got)
	}

	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("parent messages = %d, want user, assistant, tool, assistant", len(messages))
	}
	if messages[2].Role != llm.RoleTool || messages[2].ToolCallID != "sub1" {
		t.Fatalf("unexpected subagent result: %+v", messages[2])
	}
	if messages[2].Text() != "42" {
		t.Errorf("subagent result text = %q", messages[2].Text())
	}
	if messages[2].Data == nil || !strings.Contains(string(messages[2].Data), `"text":"42"`) {
		t.Errorf("subagent response data missing: %s", messages[2].Data)
	}
	assertNoDanglingToolCalls(t, messages)

	parentRequest := parentModel.Calls()[0]
	if len(parentRequest.Tools) != 1 || parentRequest.Tools[0].Name != "math_helper" {
		t.Fatalf("parent did not expose the subagent tool: %+v", parentRequest.Tools)
	}
}

func TestAgent_AsToolCustomNameAndDescription(t *testing.T) {
	sub := mustAgent(t, agent.Config{Name: "helper", Model: &scriptModel{steps: []step{finalText("ok")}}})
	subTool := sub.AsTool(agent.AsToolConfig{Name: "delegate", Description: "custom"})

	spec := subTool.Spec()
	if spec.Name != "delegate" || spec.Description != "custom" {
		t.Fatalf("unexpected spec: %+v", spec)
	}
}

func TestAgent_RejectsDuplicateTools(t *testing.T) {
	echo := newEchoTool(t, "echo", nil)
	_, err := agent.New(agent.Config{
		Name:  "dupes",
		Model: &scriptModel{steps: []step{finalText("ok")}},
		Tools: []tool.Tool{echo, echo},
	})
	if err == nil {
		t.Fatal("expected an error for duplicate tool names")
	}
}
