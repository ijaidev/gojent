// Package main demonstrates using the Google Gen AI provider with a Gojent
// agent for non-streaming and streaming generation.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/providers/google"
)

func run() error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		fmt.Println("GEMINI_API_KEY is not set.")
		fmt.Println("  export GEMINI_API_KEY=\"your-gemini-api-key\"")
		return nil
	}

	ctx := context.Background()
	model, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: apiKey,
	})
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	bot, err := agent.New(agent.Config{
		Name:         "gemini-assistant",
		Instructions: "You are a concise, helpful assistant powered by Google Gemini.",
		Model:        model,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	fmt.Println("=== 1. Non-streaming generation ===")
	prompt := "In one short sentence, what makes Go great for systems programming?"
	fmt.Printf("User: %s\n", prompt)
	response, err := bot.Generate(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	fmt.Printf("Agent: %s\n[usage] prompt=%d completion=%d total=%d\n\n",
		response.Text,
		response.Usage.PromptTokens,
		response.Usage.CompletionTokens,
		response.Usage.TotalTokens,
	)

	fmt.Println("=== 2. Streaming generation ===")
	streamPrompt := "List three key features of Go with a brief note on each."
	fmt.Printf("User: %s\nAgent: ", streamPrompt)
	run, err := bot.Stream(ctx, streamPrompt)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}
	for event := range run.Events() {
		switch typed := event.(type) {
		case agent.TextDelta:
			fmt.Print(typed.Text)
		case agent.RunEnd:
			if typed.Err != nil {
				fmt.Printf("\n[error] %v\n", typed.Err)
			}
		}
	}
	final, err := run.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	fmt.Printf("\n[usage] total tokens: %d\n", final.Usage.TotalTokens)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
