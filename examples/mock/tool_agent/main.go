// Package main demonstrates an autonomous agent with custom tools using a
// mock model, including a streamed tool-call loop.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/mockllm"
)

// CalculatorInput specifies arguments for arithmetic operations.
type CalculatorInput struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, or divide"`
	A         float64 `json:"a" description:"The first operand"`
	B         float64 `json:"b" description:"The second operand"`
}

// CalculatorOutput contains the computed arithmetic result.
type CalculatorOutput struct {
	Operation string  `json:"operation"`
	Result    float64 `json:"result"`
}

// WeatherInput specifies arguments for querying weather conditions.
type WeatherInput struct {
	City  string `json:"city" description:"The city name, e.g. Tokyo"`
	Units string `json:"units,omitempty" description:"celsius or fahrenheit"`
}

// WeatherOutput contains the weather report for a city.
type WeatherOutput struct {
	City        string  `json:"city"`
	Temperature float64 `json:"temperature"`
	Units       string  `json:"units"`
	Condition   string  `json:"condition"`
}

func calculatorTool() tool.Tool {
	return tool.MustNew(tool.Config[CalculatorInput, CalculatorOutput]{
		ID:          "calculator",
		Description: "Perform basic arithmetic operations.",
		Execute: func(_ context.Context, in CalculatorInput) (CalculatorOutput, error) {
			var result float64
			switch in.Operation {
			case "add":
				result = in.A + in.B
			case "subtract":
				result = in.A - in.B
			case "multiply":
				result = in.A * in.B
			case "divide":
				if in.B == 0 {
					return CalculatorOutput{}, errors.New("division by zero")
				}
				result = in.A / in.B
			default:
				return CalculatorOutput{}, fmt.Errorf("unsupported operation: %s", in.Operation)
			}
			fmt.Printf("  [tool] calculator(%s, %v, %v) = %v\n", in.Operation, in.A, in.B, result)
			return CalculatorOutput{Operation: in.Operation, Result: result}, nil
		},
	})
}

func weatherTool() tool.Tool {
	return tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID:          "get_weather",
		Description: "Retrieve current weather conditions for a city.",
		Execute: func(_ context.Context, in WeatherInput) (WeatherOutput, error) {
			units := in.Units
			if units == "" {
				units = "celsius"
			}
			fmt.Printf("  [tool] get_weather(%s)\n", in.City)
			return WeatherOutput{
				City:        in.City,
				Temperature: 21.5,
				Units:       units,
				Condition:   "Sunny with light breeze",
			}, nil
		},
	})
}

func runNonStreaming(ctx context.Context) error {
	fmt.Println("=== Demo 1: Autonomous tool loop (non-streaming) ===")

	turn := 0
	model := mockllm.New().ApplyOptions(
		mockllm.WithCustomStreamHandler(func(ctx context.Context, _ *llm.Request) (llm.Stream, error) {
			turn++
			if turn == 1 {
				usage := llm.Usage{PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40}
				return llm.NewSliceStream(ctx, []*llm.StreamChunk{
					{
						ToolCalls: []llm.ToolCall{{
							Index:     0,
							ID:        "call_calc",
							Name:      "calculator",
							Arguments: `{"operation":"multiply","a":42,"b":7}`,
						}},
						FinishReason: llm.FinishReasonTool,
						Usage:        &usage,
					},
				}), nil
			}
			usage := llm.Usage{PromptTokens: 45, CompletionTokens: 10, TotalTokens: 55}
			return llm.NewSliceStream(ctx, []*llm.StreamChunk{
				{Delta: "42 multiplied by 7 is 294."},
				{FinishReason: llm.FinishReasonStop, Usage: &usage},
			}), nil
		}),
	)

	bot, err := agent.New(agent.Config{
		Name:         "math-assistant",
		Instructions: "You are a math assistant with access to a calculator.",
		Model:        model,
		Tools:        []tool.Tool{calculatorTool()},
	})
	if err != nil {
		return fmt.Errorf("create calc agent: %w", err)
	}

	prompt := "What is 42 multiplied by 7?"
	fmt.Printf("User: %s\n", prompt)
	response, err := bot.Generate(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	fmt.Printf("Agent: %s\n[usage] %d total tokens\n\n", response.Text, response.Usage.TotalTokens)

	fmt.Println("--- trajectory ---")
	for i := range response.Messages {
		message := &response.Messages[i]
		switch message.Role {
		case llm.RoleAssistant:
			if len(message.ToolCalls) > 0 {
				call := message.ToolCalls[0]
				fmt.Printf("  %d assistant -> %s(%s)\n", i+1, call.Name, call.Arguments)
			} else {
				fmt.Printf("  %d assistant -> %s\n", i+1, message.Text())
			}
		case llm.RoleTool:
			fmt.Printf("  %d tool %s -> %s\n", i+1, message.Name, message.Text())
		default:
			fmt.Printf("  %d %s -> %s\n", i+1, message.Role, message.Text())
		}
	}
	fmt.Println()
	return nil
}

func runStreaming(ctx context.Context) error {
	fmt.Println("=== Demo 2: Streamed tool arguments and final answer ===")

	turn := 0
	model := mockllm.New().ApplyOptions(
		mockllm.WithCustomStreamHandler(func(ctx context.Context, _ *llm.Request) (llm.Stream, error) {
			turn++
			usage := llm.Usage{PromptTokens: 45, CompletionTokens: 10, TotalTokens: 55}
			if turn == 1 {
				return llm.NewSliceStream(ctx, []*llm.StreamChunk{
					{ToolCalls: []llm.ToolCall{{Index: 0, ID: "call_weather", Name: "get_weather", Arguments: `{"city":"`}}},
					{ToolCalls: []llm.ToolCall{{Index: 0, Arguments: `Tokyo",`}}},
					{ToolCalls: []llm.ToolCall{{Index: 0, Arguments: `"units":"celsius"}`}}, FinishReason: llm.FinishReasonTool, Usage: &usage},
				}), nil
			}
			return llm.NewSliceStream(ctx, []*llm.StreamChunk{
				{Delta: "The current "},
				{Delta: "weather in Tokyo is "},
				{Delta: "21.5°C and sunny."},
				{FinishReason: llm.FinishReasonStop, Usage: &usage},
			}), nil
		}),
	)

	bot, err := agent.New(agent.Config{
		Name:         "weather-assistant",
		Instructions: "You are a weather assistant.",
		Model:        model,
		Tools:        []tool.Tool{weatherTool()},
	})
	if err != nil {
		return fmt.Errorf("create weather agent: %w", err)
	}

	prompt := "How is the weather in Tokyo?"
	fmt.Printf("User: %s\nAgent: ", prompt)
	run, err := bot.Stream(ctx, prompt)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}

	for event := range run.Events() {
		switch typed := event.(type) {
		case agent.TextDelta:
			fmt.Print(typed.Text)
			time.Sleep(20 * time.Millisecond)
		case agent.ToolCallDelta:
			if typed.Arguments != "" {
				fmt.Printf("\n  [streamed argument] %s", typed.Arguments)
			}
		case agent.ToolStart:
			fmt.Printf("\n  [tool start] %s(%s)", typed.Call.Name, typed.Call.Arguments)
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
	fmt.Printf("\n[finish] %s | total tokens: %d\n", response.FinishReason, response.Usage.TotalTokens)
	for _, call := range response.ToolCalls {
		fmt.Printf("  invoked %s with %s\n", call.Name, call.Arguments)
	}
	return nil
}

func main() {
	ctx := context.Background()
	if err := runNonStreaming(ctx); err != nil {
		log.Fatal(err)
	}
	if err := runStreaming(ctx); err != nil {
		log.Fatal(err)
	}
}
