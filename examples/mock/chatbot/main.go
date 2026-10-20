// Package main demonstrates running an interactive, streaming, and stateful chatbot
// using Gojent with a mock LLM.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ijaidev/gojent/examples/chatbot"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func main() {
	// 1. Create a mock LLM with canned responses to simulate conversation
	mockLLM := mockllm.New(
		"Hello! I am an AI chatbot built with Gojent. I remember our conversation and stream responses in real-time.",
		"Gojent provides a lightweight, modular agent framework in Go.",
		"You can plug any LLM provider into this chatbot (like Google Gemini or OpenAI) without changing any chat logic!",
		"Goodbye! Have a great day!",
	)

	fmt.Println("Starting Gojent Mock Chatbot Demo...")

	// 2. Run the chatbot with custom options
	err := chatbot.Run(context.Background(), mockLLM,
		chatbot.WithAgentName("GojentBot"),
		chatbot.WithInstructions("You are a helpful Gojent assistant demonstrating interactive chat."),
	)
	if err != nil {
		log.Fatalf("Chatbot terminated with error: %v", err)
	}
}
