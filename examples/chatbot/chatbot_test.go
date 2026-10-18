package chatbot_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/examples/chatbot"
)

func TestRun_NilModel(t *testing.T) {
	err := chatbot.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error with nil model, got nil")
	}
}

func TestRun_ExitImmediate(t *testing.T) {
	mockLLM := llm.NewMockLLM("Never called")
	input := strings.NewReader("exit\n")
	output := &bytes.Buffer{}

	err := chatbot.Run(context.Background(), mockLLM,
		chatbot.WithIO(input, output),
		chatbot.WithBanner("Test Banner"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outStr := output.String()
	if !strings.Contains(outStr, "Test Banner") {
		t.Errorf("expected banner in output, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Goodbye!") {
		t.Errorf("expected goodbye in output, got:\n%s", outStr)
	}
}

func TestRun_MultiTurnMemory(t *testing.T) {
	mockLLM := llm.NewMockLLM(
		"Hello Alice! Nice to meet you.",
		"Your name is Alice.",
	)

	input := strings.NewReader("Hello, my name is Alice.\nWhat is my name?\nquit\n")
	output := &bytes.Buffer{}
	store := storage.NewMockStorage()
	threadID := "alice-chat-123"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := chatbot.Run(ctx, mockLLM,
		chatbot.WithIO(input, output),
		chatbot.WithStorage(store),
		chatbot.WithThreadID(threadID),
		chatbot.WithAgentName("Bot"),
	)
	if err != nil {
		t.Fatalf("chatbot.Run returned unexpected error: %v", err)
	}

	outStr := output.String()
	if !strings.Contains(outStr, "Hello Alice! Nice to meet you.") {
		t.Errorf("expected first response in output, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Your name is Alice.") {
		t.Errorf("expected second response in output, got:\n%s", outStr)
	}

	// Verify messages were persisted in storage
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID: threadID,
	})
	if err != nil {
		t.Fatalf("failed to list messages from storage: %v", err)
	}

	// Turn 1: user + assistant (2 msgs)
	// Turn 2: user + assistant (2 msgs)
	// Total: 4 messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages persisted in thread, got %d", len(msgs))
	}

	if msgs[0].Role != llm.RoleUser || msgs[0].Text() != "Hello, my name is Alice." {
		t.Errorf("unexpected message 0: %+v", msgs[0])
	}
	if msgs[1].Role != llm.RoleAssistant || msgs[1].Text() != "Hello Alice! Nice to meet you." {
		t.Errorf("unexpected message 1: %+v", msgs[1])
	}
	if msgs[2].Role != llm.RoleUser || msgs[2].Text() != "What is my name?" {
		t.Errorf("unexpected message 2: %+v", msgs[2])
	}
	if msgs[3].Role != llm.RoleAssistant || msgs[3].Text() != "Your name is Alice." {
		t.Errorf("unexpected message 3: %+v", msgs[3])
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	mockLLM := llm.NewMockLLM("Response")
	input := strings.NewReader("Hello\n")
	output := &bytes.Buffer{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running

	err := chatbot.Run(ctx, mockLLM,
		chatbot.WithIO(input, output),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", err)
	}
}
