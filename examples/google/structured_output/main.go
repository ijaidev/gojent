// Package main demonstrates structured output generation using the Google Gen AI provider with Gojent.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/gojent"
	"github.com/ijaidev/gojent/providers/google"
)

// CharacterSheet defines the target structured schema for an RPG character.
type CharacterSheet struct {
	Name      string   `json:"name" description:"Full character name"`
	Class     string   `json:"class" enum:"warrior,mage,rogue,cleric" description:"RPG class category"`
	Level     int      `json:"level" description:"Starting level (1-20)"`
	Abilities []string `json:"abilities" description:"List of 3 primary special abilities"`
	Backstory string   `json:"backstory" description:"Brief 2-sentence background story"`
}

func run() error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		fmt.Println("GEMINI_API_KEY is not set.")
		fmt.Println("To run this example with a live Gemini model, set your API key:")
		fmt.Println("  export GEMINI_API_KEY=\"your-gemini-api-key\"")
		fmt.Println("  go run ./examples/google/structured_output/main.go")
		return nil
	}

	ctx := context.Background()

	// 1. Initialize Google Gen AI model provider
	geminiModel, err := google.New(ctx, google.Config{
		Model:  "gemini-3.8-flash",
		APIKey: apiKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create Google Gen AI provider: %w", err)
	}

	// 2. Configure Agent
	charAgent, err := agent.New(agent.Config{
		ID:           "character-creator",
		Name:         "Character Creator",
		Instructions: "You are a creative tabletop RPG dungeon master specializing in character creation.",
		Model:        geminiModel,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// 3. Register with root Gojent container
	app := gojent.New(
		gojent.WithAgent(charAgent),
	)

	bot, err := app.GetAgent("character-creator")
	if err != nil {
		return fmt.Errorf("failed to retrieve agent: %w", err)
	}

	fmt.Println("=== Generating Typed Character Sheet via GenerateStructured ===")
	prompt := "Create a cunning level 4 shadow rogue who specializes in stealth and daggers."

	res, err := agent.GenerateStructured[CharacterSheet](ctx, bot, prompt)
	if err != nil {
		return fmt.Errorf("GenerateStructured failed: %w", err)
	}

	// Access strongly typed fields directly without manual JSON parsing:
	sheet := res.Data
	fmt.Printf("Name:      %s\n", sheet.Name)
	fmt.Printf("Class:     %s\n", sheet.Class)
	fmt.Printf("Level:     %d\n", sheet.Level)
	fmt.Printf("Abilities: %v\n", sheet.Abilities)
	fmt.Printf("Backstory: %s\n", sheet.Backstory)
	fmt.Printf("Tokens:    %d prompt + %d completion = %d total\n",
		res.Response.Usage.PromptTokens,
		res.Response.Usage.CompletionTokens,
		res.Response.Usage.TotalTokens,
	)

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
