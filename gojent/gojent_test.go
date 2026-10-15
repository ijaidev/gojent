package gojent_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/gojent"
)

func TestGojent_RootContainer(t *testing.T) {
	mock := llm.NewMockLLM("Welcome to Gojent!")

	bot, err := agent.New(agent.Config{
		ID:           "greeter",
		Name:         "Greeter Agent",
		Instructions: "Greet users warmly.",
		Model:        mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	app := gojent.New(
		gojent.WithAgent(bot),
	)

	// Test GetAgent
	retrieved, err := app.GetAgent("greeter")
	if err != nil {
		t.Fatalf("failed to get agent: %v", err)
	}
	if retrieved.ID() != "greeter" {
		t.Errorf("expected greeter, got %s", retrieved.ID())
	}

	// Test Generate through retrieved agent
	res, err := retrieved.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}
	if res.Text != "Welcome to Gojent!" {
		t.Errorf("expected 'Welcome to Gojent!', got '%s'", res.Text)
	}

	// Test ListAgents
	agents := app.ListAgents()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}

	// Test Non-existent agent
	_, err = app.GetAgent("unknown")
	if err == nil {
		t.Error("expected error for non-existent agent, got nil")
	}
}
