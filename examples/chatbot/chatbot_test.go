package chatbot_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/examples/chatbot"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestRun_NilModel(t *testing.T) {
	if err := chatbot.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error with nil model")
	}
}

func TestRun_ExitImmediate(t *testing.T) {
	model := mockllm.New("Never called")
	output := &bytes.Buffer{}

	err := chatbot.Run(context.Background(), model,
		chatbot.WithIO(strings.NewReader("exit\n"), output),
		chatbot.WithBanner("Test Banner"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := output.String()
	if !strings.Contains(out, "Test Banner") {
		t.Errorf("expected banner in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Goodbye!") {
		t.Errorf("expected goodbye in output, got:\n%s", out)
	}
	if model.CallCount() != 0 {
		t.Errorf("model was called %d times", model.CallCount())
	}
}

func TestRun_MultiTurnMemory(t *testing.T) {
	model := mockllm.New(
		"Hello Alice! Nice to meet you.",
		"Your name is Alice.",
	)
	input := strings.NewReader("Hello, my name is Alice.\nWhat is my name?\nquit\n")
	output := &bytes.Buffer{}
	store := session.NewMemory()
	sessionID := "alice-chat-123"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := chatbot.Run(ctx, model,
		chatbot.WithIO(input, output),
		chatbot.WithStore(store),
		chatbot.WithSessionID(sessionID),
		chatbot.WithAgentName("Bot"),
	)
	if err != nil {
		t.Fatalf("chatbot.Run returned unexpected error: %v", err)
	}

	out := output.String()
	if !strings.Contains(out, "Hello Alice! Nice to meet you.") {
		t.Errorf("expected first response in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Your name is Alice.") {
		t.Errorf("expected second response in output, got:\n%s", out)
	}

	messages, err := store.Load(ctx, session.Session{ID: sessionID})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	want := []struct {
		role llm.Role
		text string
	}{
		{llm.RoleUser, "Hello, my name is Alice."},
		{llm.RoleAssistant, "Hello Alice! Nice to meet you."},
		{llm.RoleUser, "What is my name?"},
		{llm.RoleAssistant, "Your name is Alice."},
	}
	for i, expected := range want {
		if messages[i].Role != expected.role || messages[i].Text() != expected.text {
			t.Errorf("message %d = %+v, want %s %q", i, messages[i], expected.role, expected.text)
		}
	}

	// The second model call must replay the first exchange.
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	if len(calls[1].Messages) != 4 {
		t.Fatalf("second request messages = %d, want system, user, assistant, user", len(calls[1].Messages))
	}
	if calls[1].Messages[3].Role != llm.RoleUser || calls[1].Messages[3].Text() != "What is my name?" {
		t.Errorf("second request did not include the new input: %+v", calls[1].Messages)
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	model := mockllm.New("Response")
	output := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := chatbot.Run(ctx, model, chatbot.WithIO(strings.NewReader("Hello\n"), output))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
