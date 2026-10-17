// Package main demonstrates running an interactive, multi-turn, streaming chatbot
// powered by Google Gemini and Gojent.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ijaidev/gojent/examples/chatbot"
	"github.com/ijaidev/gojent/providers/google"
)

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		fmt.Println("GEMINI_API_KEY is not set.")
		fmt.Println("To run this interactive chatbot with Google Gemini, set your API key:")
		fmt.Println("  export GEMINI_API_KEY=\"your-gemini-api-key\"")
		fmt.Println("  go run ./examples/google/chatbot/main.go")
		return
	}

	ctx := context.Background()

	// 1. Initialize Google Gen AI provider (Gemini)
	geminiModel, err := google.New(ctx, google.Config{
		Model:  "gemini-3.8-flash",
		APIKey: apiKey,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Google Gemini provider: %v", err)
	}

	// 2. Run the chatbot using the swappable chatbot.Run function
	err = chatbot.Run(ctx, geminiModel,
		chatbot.WithAgentName("Gemini"),
		chatbot.WithInstructions("You are a friendly, concise, and helpful AI assistant powered by Google Gemini."),
	)
	if err != nil {
		log.Fatalf("Chatbot error: %v", err)
	}
}
