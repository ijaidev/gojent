package hitl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/tools/hitl"
)

func TestAskUserTool_CustomPromptHandler(t *testing.T) {
	askTool, err := hitl.NewAskUserTool(hitl.Config{
		PromptHandler: func(_ context.Context, question string, _ []string) (string, error) {
			if question != "Select sauce" {
				t.Errorf("unexpected question: %s", question)
			}
			return "Tomato", nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create ask_user tool: %v", err)
	}

	inputBytes, _ := json.Marshal(hitl.AskUserInput{
		Question: "Select sauce",
		Options:  []string{"Tomato", "Pesto", "Alfredo"},
	})

	resAny, err := askTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	res := resAny.(hitl.AskUserOutput)
	if res.Answer != "Tomato" {
		t.Errorf("expected Tomato, got %s", res.Answer)
	}
}

func TestAskUserTool_ReaderStream(t *testing.T) {
	inBuf := strings.NewReader("Yes, proceed\n")
	var outBuf bytes.Buffer

	askTool, err := hitl.NewAskUserTool(hitl.Config{
		Reader: inBuf,
		Writer: &outBuf,
	})
	if err != nil {
		t.Fatalf("failed to create ask_user tool: %v", err)
	}

	inputBytes, _ := json.Marshal(hitl.AskUserInput{
		Question: "Are you sure?",
	})

	resAny, err := askTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	res := resAny.(hitl.AskUserOutput)
	if res.Answer != "Yes, proceed" {
		t.Errorf("expected 'Yes, proceed', got %s", res.Answer)
	}
}
