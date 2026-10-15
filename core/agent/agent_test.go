package agent_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
)

func TestAgent_Validation(t *testing.T) {
	_, err := agent.New(agent.Config{})
	if err == nil {
		t.Error("expected error when ID is empty, got nil")
	}

	_, err = agent.New(agent.Config{
		ID: "test-agent",
	})
	if err == nil {
		t.Error("expected error when Model is nil, got nil")
	}
}

func TestAgent_Generate_WithMockLLM(t *testing.T) {
	mock := llm.NewMockLLM("I can help you with recipes!")

	chef, err := agent.New(agent.Config{
		ID:           "chef-agent",
		Name:         "Chef Agent",
		Instructions: "You are a professional chef.",
		Model:        mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()
	res, err := chef.Generate(ctx, "What can I cook with tomatoes?")
	if err != nil {
		t.Fatalf("agent.Generate returned error: %v", err)
	}

	if res.Text != "I can help you with recipes!" {
		t.Errorf("unexpected output text: %s", res.Text)
	}

	// Verify that the LLM received the instructions as system message and prompt as user message
	lastCall := mock.LastCall()
	if lastCall == nil {
		t.Fatal("expected mock to record call, got nil")
	}
	if len(lastCall.Messages) != 2 {
		t.Fatalf("expected 2 messages sent to model, got %d", len(lastCall.Messages))
	}
	if lastCall.Messages[0].Role != llm.RoleSystem || lastCall.Messages[0].Content != "You are a professional chef." {
		t.Errorf("expected system instructions, got %+v", lastCall.Messages[0])
	}
	if lastCall.Messages[1].Role != llm.RoleUser || lastCall.Messages[1].Content != "What can I cook with tomatoes?" {
		t.Errorf("expected user message, got %+v", lastCall.Messages[1])
	}

	// Verify the returned conversation history
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 conversation messages in response, got %d", len(res.Messages))
	}
	if res.Messages[2].Role != llm.RoleAssistant || res.Messages[2].Content != "I can help you with recipes!" {
		t.Errorf("expected assistant response in messages, got %+v", res.Messages[2])
	}
}

func TestAgent_SwapModel(t *testing.T) {
	mock1 := llm.NewMockLLM("Response from Model 1")
	mock2 := llm.NewMockLLM("Response from Model 2")

	a, err := agent.New(agent.Config{
		ID:    "agent-swap",
		Model: mock1,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	// 1. First run with Model 1
	res1, err := a.Generate(ctx, "ping")
	if err != nil || res1.Text != "Response from Model 1" {
		t.Fatalf("expected response from model 1, got %v, err: %v", res1, err)
	}

	// 2. Swap model to Model 2
	err = a.SetModel(mock2)
	if err != nil {
		t.Fatalf("failed to swap model: %v", err)
	}

	// 3. Second run with swapped Model 2
	res2, err := a.Generate(ctx, "ping")
	if err != nil || res2.Text != "Response from Model 2" {
		t.Fatalf("expected response from model 2, got %v, err: %v", res2, err)
	}
}

func TestAgent_PerCallModelOverride(t *testing.T) {
	defaultMock := llm.NewMockLLM("Default response")
	overrideMock := llm.NewMockLLM("Overridden response")

	a, err := agent.New(agent.Config{
		ID:    "agent-override",
		Model: defaultMock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	// Call with model override
	res, err := a.Generate(ctx, "hello", agent.WithModel(overrideMock))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "Overridden response" {
		t.Errorf("expected overridden response, got %s", res.Text)
	}

	// Verify default model wasn't called, override model was
	if defaultMock.CallCount() != 0 {
		t.Errorf("expected default mock not to be called, got count %d", defaultMock.CallCount())
	}
	if overrideMock.CallCount() != 1 {
		t.Errorf("expected override mock to be called once, got count %d", overrideMock.CallCount())
	}
}
