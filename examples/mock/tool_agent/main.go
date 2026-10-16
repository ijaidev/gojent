// Package main demonstrates building an autonomous Agent equipped with custom tools
// using a Mock LLM to simulate tool calling, ReAct execution loops, and real-time
// tool argument streaming.
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
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/gojent"
)

// CalculatorInput specifies arguments for arithmetic operations.
type CalculatorInput struct {
	Operation string  `json:"operation" description:"The arithmetic operation to perform: add, subtract, multiply, or divide"`
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
	City  string `json:"city" description:"The city name, e.g. Tokyo, Paris, San Francisco"`
	Units string `json:"units,omitempty" description:"The temperature scale: celsius or fahrenheit"`
}

// WeatherOutput contains the weather report for a city.
type WeatherOutput struct {
	City        string  `json:"city"`
	Temperature float64 `json:"temperature"`
	Units       string  `json:"units"`
	Condition   string  `json:"condition"`
}

func run() error {
	ctx := context.Background()

	// 1. Define strongly typed tools using tool.MustNew
	calcTool := tool.MustNew(tool.Config[CalculatorInput, CalculatorOutput]{
		ID:          "calculator",
		Description: "Perform basic mathematical arithmetic operations (add, subtract, multiply, divide).",
		Execute: func(_ context.Context, in CalculatorInput) (CalculatorOutput, error) {
			var res float64
			switch in.Operation {
			case "add":
				res = in.A + in.B
			case "subtract":
				res = in.A - in.B
			case "multiply":
				res = in.A * in.B
			case "divide":
				if in.B == 0 {
					return CalculatorOutput{}, errors.New("division by zero")
				}
				res = in.A / in.B
			default:
				return CalculatorOutput{}, fmt.Errorf("unsupported operation: %s", in.Operation)
			}
			return CalculatorOutput{
				Operation: in.Operation,
				Result:    res,
			}, nil
		},
	})

	weatherTool := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID:          "get_weather",
		Description: "Retrieve current weather conditions for a specified city.",
		Execute: func(_ context.Context, in WeatherInput) (WeatherOutput, error) {
			units := in.Units
			if units == "" {
				units = "celsius"
			}
			return WeatherOutput{
				City:        in.City,
				Temperature: 21.5,
				Units:       units,
				Condition:   "Sunny with light breeze",
			}, nil
		},
	})

	// =========================================================================
	// Demo 1: Autonomous ReAct loop with MockLLM (Non-streaming)
	// =========================================================================
	fmt.Println("=== Demo 1: Autonomous ReAct Tool Execution (Non-streaming) ===")

	calcTurn := 0
	mockCalc := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			calcTurn++
			if calcTurn == 1 {
				// Turn 1: Model inspects prompt and returns a ToolCall request
				fmt.Println("  [MockLLM] Decided to invoke tool: calculator(operation=\"multiply\", a=42, b=7)")
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls: []llm.ToolCall{
						{
							ID:        "call_calc_42_7",
							Name:      "calculator",
							Arguments: `{"operation":"multiply","a":42,"b":7}`,
						},
					},
					Usage: llm.Usage{PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40},
				}, nil
			}

			// Turn 2: Model receives tool result from Agent and synthesizes final answer
			fmt.Println("  [MockLLM] Received tool execution result. Generating final response.")
			return &llm.GenerateResponse{
				Text:         "42 multiplied by 7 is 294.",
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 45, CompletionTokens: 10, TotalTokens: 55},
			}, nil
		}),
	)

	// Create Agent with registered tools
	calcBot, err := agent.New(agent.Config{
		ID:           "math-assistant",
		Name:         "Math Assistant",
		Instructions: "You are a helpful math assistant with access to a calculator tool.",
		Model:        mockCalc,
		Tools:        []tool.Tool{calcTool},
	})
	if err != nil {
		return fmt.Errorf("failed to create calc agent: %w", err)
	}

	app := gojent.New(gojent.WithAgent(calcBot))
	agent1, err := app.GetAgent("math-assistant")
	if err != nil {
		return fmt.Errorf("failed to get agent: %w", err)
	}

	prompt1 := "What is 42 multiplied by 7?"
	fmt.Printf("User: %s\n", prompt1)

	res1, err := agent1.Generate(ctx, prompt1)
	if err != nil {
		return fmt.Errorf("agent.Generate failed: %w", err)
	}

	fmt.Printf("Agent: %s\n", res1.Text)
	fmt.Printf("[Cumulative Usage] Total tokens: %d\n", res1.Usage.TotalTokens)

	fmt.Println("\n--- Conversation ReAct Trajectory ---")
	for i, msg := range res1.Messages {
		switch msg.Role {
		case llm.RoleSystem:
			fmt.Printf("  Step %d [%s]: %s\n", i+1, msg.Role, msg.Content)
		case llm.RoleUser:
			fmt.Printf("  Step %d [%s]: %s\n", i+1, msg.Role, msg.Content)
		case llm.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				fmt.Printf("  Step %d [%s Tool Call]: Name=%s, Args=%s\n",
					i+1, msg.Role, msg.ToolCalls[0].Name, msg.ToolCalls[0].Arguments)
			} else {
				fmt.Printf("  Step %d [%s Final Reply]: %s\n", i+1, msg.Role, msg.Content)
			}
		case llm.RoleTool:
			fmt.Printf("  Step %d [%s Result]: Name=%s, Output=%s\n",
				i+1, msg.Role, msg.Name, msg.Content)
		}
	}

	// =========================================================================
	// Demo 2: Streaming ReAct loop with Real-time Tool Argument Streaming
	// =========================================================================
	fmt.Println("\n=== Demo 2: Streaming ReAct with Tool Argument Streaming ===")

	streamTurn := 0
	mockStream := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomStreamHandler(func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			streamTurn++
			if streamTurn == 1 {
				// Turn 1: Model streams the tool arguments in real-time chunks
				chunks := []*llm.StreamChunk{
					{
						ToolCalls: []llm.ToolCall{
							{ID: "call_weather_tokyo", Name: "get_weather", Arguments: `{"city":"`},
						},
					},
					{
						ToolCalls: []llm.ToolCall{
							{ID: "call_weather_tokyo", Arguments: `Tokyo",`},
						},
					},
					{
						ToolCalls: []llm.ToolCall{
							{ID: "call_weather_tokyo", Arguments: `"units":"celsius"}`},
						},
						FinishReason: llm.FinishReasonTool,
					},
				}
				return llm.NewSliceStream(ctx, chunks), nil
			}

			// Turn 2: After tool execution, model streams the final answer tokens
			chunks := []*llm.StreamChunk{
				{Delta: "The current "},
				{Delta: "weather in "},
				{Delta: "Tokyo is "},
				{Delta: "21.5°C and "},
				{Delta: "Sunny with light breeze."},
			}
			return llm.NewSliceStream(ctx, chunks), nil
		}),
	)

	weatherBot, err := agent.New(agent.Config{
		ID:           "weather-assistant",
		Name:         "Weather Assistant",
		Instructions: "You are a helpful assistant with weather lookup capabilities.",
		Model:        mockStream,
		Tools:        []tool.Tool{weatherTool},
	})
	if err != nil {
		return fmt.Errorf("failed to create weather agent: %w", err)
	}

	prompt2 := "How is the weather in Tokyo?"
	fmt.Printf("User: %s\n", prompt2)

	stream, err := weatherBot.Stream(ctx, prompt2)
	if err != nil {
		return fmt.Errorf("weatherBot.Stream failed: %w", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	fmt.Println("Streaming output:")
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream chunk error: %w", err)
		}

		// Streamed tool argument deltas can be inspected live
		if len(chunk.ToolCalls) > 0 {
			for _, tc := range chunk.ToolCalls {
				if tc.Arguments != "" {
					fmt.Printf("  [Streaming Tool Arg Delta] %s\n", tc.Arguments)
				}
			}
		}

		// Streamed text deltas from the final answer
		if chunk.Delta != "" {
			fmt.Print(chunk.Delta)
			time.Sleep(30 * time.Millisecond)
		}
	}
	fmt.Println()

	// Inspect final assembled response
	finalRes, err := stream.Response()
	if err != nil {
		return fmt.Errorf("stream.Response failed: %w", err)
	}

	fmt.Printf("\n[Stream Response Metadata]\n")
	fmt.Printf("  Final Text: %s\n", finalRes.Text)
	fmt.Printf("  Tools Invoked: %d\n", len(finalRes.ToolCalls))
	for _, tc := range finalRes.ToolCalls {
		fmt.Printf("    - %s with arguments: %s\n", tc.Name, tc.Arguments)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
