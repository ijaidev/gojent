// Package main demonstrates creating an autonomous agent with a mock LLM and
// swapping models for a single run.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func main() {
	mockModel := mockllm.New(
		"Hello! I am your AI assistant built with Gojent.",
		"I can help you build autonomous agents in Go.",
	)

	bot, err := agent.New(agent.Config{
		Name:         "assistant",
		Description:  "A helpful, concise assistant",
		Instructions: "You are a helpful, concise AI assistant.",
		Model:        mockModel,
	})
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	first, err := bot.Generate(ctx, "Who are you?")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: Who are you?\nAgent: %s\n\n", first.Text)

	second, err := bot.Generate(ctx, "What can you do?")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: What can you do?\nAgent: %s\n\n", second.Text)

	// Swap the model for one run without mutating the agent.
	specialized := mockllm.New("Dice tomatoes, add olive oil and fresh basil.")
	fmt.Println("--- Using a different model for one run ---")
	third, err := bot.Generate(ctx, "Give me a recipe with tomatoes.", agent.WithModel(specialized))
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: Give me a recipe with tomatoes.\nAgent (swapped model): %s\n", third.Text)
}
