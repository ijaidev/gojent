// Package main demonstrates creating an autonomous agent with a mock LLM and runtime model swapping.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func main() {
	// 1. Create a swappable mock LLM with canned responses
	mockLLM := mockllm.New(
		"Hello! I am your AI assistant built with Gojent.",
		"I can help you build autonomous agents and workflows in Go.",
	)

	// 2. Initialize an Agent injecting the mock LLM
	bot, err := agent.New(agent.Config{
		ID:           "assistant",
		Name:         "Helpful Assistant",
		Instructions: "You are a helpful, concise AI assistant.",
		Model:        mockLLM,
	})
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	// 3. Execute generation
	res1, err := bot.Generate(ctx, "Who are you?")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: Who are you?\nAgent: %s\n\n", res1.Text)

	// Second prompt
	res2, err := bot.Generate(ctx, "What can you do?")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: What can you do?\nAgent: %s\n\n", res2.Text)

	// 4. Demonstrate runtime model swapping!
	specializedMock := mockllm.New("Here is the recipe: Dice tomatoes, add olive oil and fresh basil.")
	fmt.Println("--- Swapping Model dynamically at runtime ---")
	_ = bot.SetModel(specializedMock)

	res3, err := bot.Generate(ctx, "Give me a recipe with tomatoes.")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("User: Give me a recipe with tomatoes.\nAgent (swapped model): %s\n", res3.Text)
}
