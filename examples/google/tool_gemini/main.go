// Package main demonstrates using Google Gen AI (Gemini) with custom tools in Gojent.
// It highlights tool registration, Gemini function calling, autonomous ReAct loops,
// and real-time streaming responses.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/google"
)

// WeatherInput defines the parameters for the get_current_weather tool.
type WeatherInput struct {
	Location string `json:"location" description:"City and country or state, e.g. Tokyo, Japan or Paris, France"`
	Unit     string `json:"unit,omitempty" description:"Temperature unit: 'celsius' or 'fahrenheit'"`
}

// WeatherReport defines the structured return data for weather conditions.
type WeatherReport struct {
	Location    string  `json:"location"`
	Temperature float64 `json:"temperature"`
	Unit        string  `json:"unit"`
	Condition   string  `json:"condition"`
	Humidity    int     `json:"humidity"`
}

// ExchangeRateInput defines the parameters for the get_exchange_rate tool.
type ExchangeRateInput struct {
	From string `json:"from" description:"Base currency code, e.g. USD, EUR, JPY"`
	To   string `json:"to" description:"Target currency code, e.g. EUR, JPY, GBP"`
}

// ExchangeRateReport defines the currency exchange rate lookup result.
type ExchangeRateReport struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Rate float64 `json:"rate"`
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
		fmt.Println("  go run ./examples/google/tool_gemini/main.go")
		return nil
	}

	ctx := context.Background()

	// 1. Define strongly typed tools using tool.MustNew
	weatherTool := tool.MustNew(tool.Config[WeatherInput, WeatherReport]{
		ID:          "get_current_weather",
		Description: "Get current weather conditions for a specified location.",
		Execute: func(_ context.Context, in WeatherInput) (WeatherReport, error) {
			unit := in.Unit
			if unit == "" {
				unit = "celsius"
			}
			temp := 22.0
			if strings.EqualFold(unit, "fahrenheit") {
				temp = 71.6
			}
			fmt.Printf("\n  -> [Tool Execution] get_current_weather called for %q (unit: %s)\n", in.Location, unit)
			return WeatherReport{
				Location:    in.Location,
				Temperature: temp,
				Unit:        unit,
				Condition:   "Sunny with light clouds",
				Humidity:    50,
			}, nil
		},
	})

	exchangeTool := tool.MustNew(tool.Config[ExchangeRateInput, ExchangeRateReport]{
		ID:          "get_exchange_rate",
		Description: "Get the current currency exchange rate between two currencies.",
		Execute: func(_ context.Context, in ExchangeRateInput) (ExchangeRateReport, error) {
			fmt.Printf("\n  -> [Tool Execution] get_exchange_rate called: %s to %s\n", in.From, in.To)
			rates := map[string]float64{
				"USD:EUR": 0.92,
				"USD:JPY": 155.40,
				"USD:GBP": 0.79,
				"EUR:USD": 1.09,
				"EUR:JPY": 168.90,
			}
			pair := fmt.Sprintf("%s:%s", strings.ToUpper(in.From), strings.ToUpper(in.To))
			rate, ok := rates[pair]
			if !ok {
				rate = 1.0
			}
			return ExchangeRateReport{
				From: strings.ToUpper(in.From),
				To:   strings.ToUpper(in.To),
				Rate: rate,
			}, nil
		},
	})

	// 2. Initialize the Google Gen AI provider with Gemini
	geminiModel, err := google.New(ctx, google.Config{
		Model:  "gemini-3.8-flash",
		APIKey: apiKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create Google Gen AI provider: %w", err)
	}

	// 3. Configure autonomous Agent equipped with tools
	toolAgent, err := agent.New(agent.Config{
		ID:           "gemini-tool-assistant",
		Name:         "Gemini Tool Assistant",
		Instructions: "You are a concise, helpful assistant with access to real-time tools. When asked for weather or currency exchange rates, always use the tools provided.",
		Model:        geminiModel,
		Tools:        []tool.Tool{weatherTool, exchangeTool},
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// =========================================================================
	// Demo 1: Autonomous ReAct Tool Execution (Non-streaming)
	// =========================================================================
	fmt.Println("=== 1. Non-streaming ReAct Generation with Tools ===")
	prompt1 := "What is the current weather in Tokyo right now in Celsius?"
	fmt.Printf("User: %s\n", prompt1)

	res1, err := toolAgent.Generate(ctx, prompt1)
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	fmt.Printf("Agent: %s\n", res1.Text)
	fmt.Printf("[Usage] Prompt Tokens: %d | Completion Tokens: %d | Total Tokens: %d\n",
		res1.Usage.PromptTokens, res1.Usage.CompletionTokens, res1.Usage.TotalTokens)

	fmt.Println("\n--- Conversation ReAct Trajectory ---")
	for i := range res1.Messages {
		msg := &res1.Messages[i]
		switch msg.Role {
		case llm.RoleSystem:
			fmt.Printf("  Step %d [%s]: %s\n", i+1, msg.Role, msg.Text())
		case llm.RoleUser:
			fmt.Printf("  Step %d [%s]: %s\n", i+1, msg.Role, msg.Text())
		case llm.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				fmt.Printf("  Step %d [%s Tool Call]: Name=%s, Args=%s\n",
					i+1, msg.Role, msg.ToolCalls[0].Name, msg.ToolCalls[0].Arguments)
			} else {
				fmt.Printf("  Step %d [%s Reply]: %s\n", i+1, msg.Role, msg.Text())
			}
		case llm.RoleTool:
			fmt.Printf("  Step %d [%s Result]: Name=%s, Content=%s\n",
				i+1, msg.Role, msg.Name, msg.Text())
		}
	}

	// =========================================================================
	// Demo 2: Real-time Streaming ReAct Execution with Tools
	// =========================================================================
	fmt.Println("\n=== 2. Real-time Streaming Generation with Tools ===")
	prompt2 := "What is the exchange rate from USD to EUR?"
	fmt.Printf("User: %s\nAgent: ", prompt2)

	stream, err := toolAgent.Stream(ctx, prompt2)
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
		if chunk.Delta != "" {
			fmt.Print(chunk.Delta)
		}
	}
	fmt.Println()

	finalRes, err := stream.Response()
	if err != nil {
		return fmt.Errorf("failed to finalize stream response: %w", err)
	}
	fmt.Printf("[Usage] Total tokens consumed across ReAct turns: %d\n", finalRes.Usage.TotalTokens)
	if len(finalRes.ToolCalls) > 0 {
		fmt.Printf("[Tools Invoked]\n")
		for _, tc := range finalRes.ToolCalls {
			fmt.Printf("  - %s (arguments: %s)\n", tc.Name, tc.Arguments)
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
