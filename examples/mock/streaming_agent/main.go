// Package main demonstrates real-time streaming with an autonomous agent.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func stream(ctx context.Context, bot *agent.Agent, prompt string) error {
	fmt.Printf("User: %s\nAgent: ", prompt)

	run, err := bot.Stream(ctx, prompt)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}

	for event := range run.Events() {
		switch typed := event.(type) {
		case agent.TextDelta:
			fmt.Print(typed.Text)
			// Simulate network pacing so the streaming is visible.
			time.Sleep(20 * time.Millisecond)
		case agent.ThinkingDelta:
			fmt.Printf("[thinking] %s", typed.Text)
		case agent.RunEnd:
			if typed.Err != nil {
				fmt.Printf("\n[error] %v\n", typed.Err)
			}
		}
	}

	response, err := run.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	fmt.Printf("\n[finish] %s | prompt=%d completion=%d total=%d\n\n",
		response.FinishReason,
		response.Usage.PromptTokens,
		response.Usage.CompletionTokens,
		response.Usage.TotalTokens,
	)
	return nil
}

func run() error {
	model := mockllm.New(
		"Hello! I am your AI assistant running on Gojent. Streaming gives users immediate feedback.",
		"Autonomous workflows in Go can be lightweight, fast, and concurrent.",
	)
	bot, err := agent.New(agent.Config{
		Name:         "stream-bot",
		Instructions: "You are a concise, helpful assistant demonstrating streaming in Go.",
		Model:        model,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	ctx := context.Background()
	if err := stream(ctx, bot, "Why is streaming important in agent frameworks?"); err != nil {
		return err
	}
	return stream(ctx, bot, "Tell me about Go concurrency.")
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
