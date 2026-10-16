// Package main demonstrates using the Google Gen AI provider with a Gojent Agent.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/gojent"
	"github.com/ijaidev/gojent/providers/google"
)

func run() error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		fmt.Println("GEMINI_API_KEY is not set.")
		fmt.Println("To run this example with a live Gemini model, set your API key:")
		fmt.Println("  export GEMINI_API_KEY=\"your-gemini-api-key\"")
		fmt.Println("  go run ./examples/google/simple_gemini/main.go")
		return nil
	}

	ctx := context.Background()

	// 1. Initialize the Google Gen AI model provider
	geminiModel, err := google.New(ctx, google.Config{
		Model:  "gemini-3.8-flash",
		APIKey: apiKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create Google Gen AI provider: %w", err)
	}

	// 2. Configure an autonomous Agent powered by Gemini
	geminiAgent, err := agent.New(agent.Config{
		ID:           "gemini-assistant",
		Name:         "Gemini Assistant",
		Instructions: "You are a concise, helpful assistant powered by Google Gemini.",
		Model:        geminiModel,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// 3. Register with root Gojent container
	app := gojent.New(
		gojent.WithAgent(geminiAgent),
	)

	bot, err := app.GetAgent("gemini-assistant")
	if err != nil {
		return fmt.Errorf("failed to retrieve agent: %w", err)
	}

	// 4. Non-streaming Generation
	fmt.Println("=== 1. Non-streaming Generation ===")
	prompt := "In one short sentence, what makes Go great for systems programming?"
	fmt.Printf("User: %s\n", prompt)

	res, err := bot.Generate(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}
	fmt.Printf("Agent: %s\n", res.Text)
	fmt.Printf("[Usage] Prompt: %d | Completion: %d | Total: %d\n\n",
		res.Usage.PromptTokens, res.Usage.CompletionTokens, res.Usage.TotalTokens)

	// 5. Real-time Streaming Generation
	fmt.Println("=== 2. Real-time Streaming Generation ===")
	streamPrompt := "List three key features of the Go language with a brief note on each."
	fmt.Printf("User: %s\nAgent: ", streamPrompt)

	stream, err := bot.Stream(ctx, streamPrompt)
	if err != nil {
		return fmt.Errorf("stream failed: %w", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream chunk error: %w", err)
		}
		fmt.Print(chunk.Delta)
	}
	fmt.Println()

	finalRes, err := stream.Response()
	if err != nil {
		return fmt.Errorf("failed to finalize stream response: %w", err)
	}
	fmt.Printf("\n[Usage] Total tokens consumed: %d\n", finalRes.Usage.TotalTokens)

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
