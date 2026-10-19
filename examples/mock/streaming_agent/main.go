// Package main demonstrates real-time streaming with an autonomous Agent using Gojent.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
)

func run() error {
	// 1. Create a mock LLM with canned responses that will be streamed word-by-word
	mockLLM := llm.NewMockLLM(
		"Hello! I am your AI assistant running on Gojent. Streaming tokens in real time gives your users immediate feedback.",
		"Autonomous workflows in Go can be lightweight, fast, and concurrent.",
	)

	// 2. Initialize an Agent configured with system instructions
	bot, err := agent.New(agent.Config{
		ID:           "stream-bot",
		Name:         "Streaming Assistant",
		Instructions: "You are a concise, helpful assistant demonstrating streaming in Go.",
		Model:        mockLLM,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	ctx := context.Background()

	// 3. Stream response using standard Recv() loop
	fmt.Println("--- Demo 1: Real-time token streaming with Recv() ---")
	fmt.Print("User: Why is streaming important in agent frameworks?\nAgent: ")

	stream1, err := bot.Stream(ctx, "Why is streaming important in agent frameworks?")
	if err != nil {
		return fmt.Errorf("failed to start stream: %w", err)
	}
	defer func() {
		_ = stream1.Close()
	}()

	for {
		chunk, err := stream1.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream chunk error: %w", err)
		}
		fmt.Print(chunk.Delta)
		// Small delay to simulate realistic network streaming
		time.Sleep(30 * time.Millisecond)
	}
	fmt.Println()

	// 4. Inspect final accumulated response and token usage
	res1, err := stream1.Response()
	if err != nil {
		return fmt.Errorf("failed to retrieve finalized response: %w", err)
	}
	fmt.Printf("[Metadata] Finish reason: %s | Tokens: %d prompt + %d completion = %d total\n\n",
		res1.FinishReason,
		res1.Usage.PromptTokens,
		res1.Usage.CompletionTokens,
		res1.Usage.TotalTokens,
	)

	// 5. Stream response using range-over-func iterator (Go 1.23+)
	fmt.Println("--- Demo 2: Streaming with range-over-func stream.All() ---")
	fmt.Print("User: Tell me about Go concurrency.\nAgent: ")

	stream2, err := bot.Stream(ctx, "Tell me about Go concurrency.")
	if err != nil {
		return fmt.Errorf("failed to start stream: %w", err)
	}
	defer func() {
		_ = stream2.Close()
	}()

	for chunk, err := range stream2.All() {
		if err != nil {
			return fmt.Errorf("iterator error: %w", err)
		}
		fmt.Print(chunk.Delta)
		time.Sleep(30 * time.Millisecond)
	}
	fmt.Println()
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
