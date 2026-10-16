package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_Validation(t *testing.T) {
	_, err := agent.New(agent.Config{})
	if err == nil {
		t.Error("expected error when ID is empty, got nil")
	}

	_, err = agent.New(agent.Config{
		ID: "test-agent",
	})
	if err == nil {
		t.Error("expected error when Model is nil, got nil")
	}
}

func TestAgent_Generate_WithMockLLM(t *testing.T) {
	mock := llm.NewMockLLM("I can help you with recipes!")

	chef, err := agent.New(agent.Config{
		ID:           "chef-agent",
		Name:         "Chef Agent",
		Instructions: "You are a professional chef.",
		Model:        mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()
	res, err := chef.Generate(ctx, "What can I cook with tomatoes?")
	if err != nil {
		t.Fatalf("agent.Generate returned error: %v", err)
	}

	if res.Text != "I can help you with recipes!" {
		t.Errorf("unexpected output text: %s", res.Text)
	}

	// Verify that the LLM received the instructions as system message and prompt as user message
	lastCall := mock.LastCall()
	if lastCall == nil {
		t.Fatal("expected mock to record call, got nil")
	}
	if len(lastCall.Messages) != 2 {
		t.Fatalf("expected 2 messages sent to model, got %d", len(lastCall.Messages))
	}
	if lastCall.Messages[0].Role != llm.RoleSystem || lastCall.Messages[0].Content != "You are a professional chef." {
		t.Errorf("expected system instructions, got %+v", lastCall.Messages[0])
	}
	if lastCall.Messages[1].Role != llm.RoleUser || lastCall.Messages[1].Content != "What can I cook with tomatoes?" {
		t.Errorf("expected user message, got %+v", lastCall.Messages[1])
	}

	// Verify the returned conversation history
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 conversation messages in response, got %d", len(res.Messages))
	}
	if res.Messages[2].Role != llm.RoleAssistant || res.Messages[2].Content != "I can help you with recipes!" {
		t.Errorf("expected assistant response in messages, got %+v", res.Messages[2])
	}
}

func TestAgent_SwapModel(t *testing.T) {
	mock1 := llm.NewMockLLM("Response from Model 1")
	mock2 := llm.NewMockLLM("Response from Model 2")

	a, err := agent.New(agent.Config{
		ID:    "agent-swap",
		Model: mock1,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	// 1. First run with Model 1
	res1, err := a.Generate(ctx, "ping")
	if err != nil || res1.Text != "Response from Model 1" {
		t.Fatalf("expected response from model 1, got %v, err: %v", res1, err)
	}

	// 2. Swap model to Model 2
	err = a.SetModel(mock2)
	if err != nil {
		t.Fatalf("failed to swap model: %v", err)
	}

	// 3. Second run with swapped Model 2
	res2, err := a.Generate(ctx, "ping")
	if err != nil || res2.Text != "Response from Model 2" {
		t.Fatalf("expected response from model 2, got %v, err: %v", res2, err)
	}
}

func TestAgent_PerCallModelOverride(t *testing.T) {
	defaultMock := llm.NewMockLLM("Default response")
	overrideMock := llm.NewMockLLM("Overridden response")

	a, err := agent.New(agent.Config{
		ID:    "agent-override",
		Model: defaultMock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()

	// Call with model override
	res, err := a.Generate(ctx, "hello", agent.WithModel(overrideMock))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "Overridden response" {
		t.Errorf("expected overridden response, got %s", res.Text)
	}

	// Verify default model wasn't called, override model was
	if defaultMock.CallCount() != 0 {
		t.Errorf("expected default mock not to be called, got count %d", defaultMock.CallCount())
	}
	if overrideMock.CallCount() != 1 {
		t.Errorf("expected override mock to be called once, got count %d", overrideMock.CallCount())
	}
}

func TestAgent_ToolRegistrationAndManagement(t *testing.T) {
	dummyTool := tool.MustNew(tool.Config[string, string]{
		ID:          "calc",
		Description: "Perform calculations",
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})

	mock := llm.NewMockLLM("ok")
	a, err := agent.New(agent.Config{
		ID:    "agent-tools",
		Model: mock,
		Tools: []tool.Tool{dummyTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// Verify tool registration
	tools := a.Tools()
	if len(tools) != 1 || tools[0].ID() != "calc" {
		t.Errorf("expected calc tool in list, got %+v", tools)
	}

	retrieved, found := a.GetTool("calc")
	if !found || retrieved.ID() != "calc" {
		t.Errorf("failed to retrieve registered tool: %+v", retrieved)
	}

	// Add nil tool fails
	err = a.AddTool(nil)
	if !errors.Is(err, agent.ErrToolNil) {
		t.Errorf("expected ErrToolNil, got %v", err)
	}

	// Add new tool
	newTool := tool.MustNew(tool.Config[string, string]{
		ID:          "greeter",
		Description: "Greets user",
		Execute: func(_ context.Context, in string) (string, error) {
			return "Hello " + in, nil
		},
	})
	err = a.AddTool(newTool)
	if err != nil {
		t.Fatalf("AddTool failed: %v", err)
	}
	if len(a.Tools()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(a.Tools()))
	}

	// Remove tool
	a.RemoveTool("calc")
	if len(a.Tools()) != 1 {
		t.Errorf("expected 1 tool after removal, got %d", len(a.Tools()))
	}
	_, found = a.GetTool("calc")
	if found {
		t.Error("expected calc tool to be removed")
	}
}

func TestAgent_Generate_PassesToolsToLLM(t *testing.T) {
	calcTool := tool.MustNew(tool.Config[string, string]{
		ID:          "calc",
		Description: "A calculator",
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})

	mock := llm.NewMockLLM("computed")
	a, err := agent.New(agent.Config{
		ID:    "agent-calc",
		Model: mock,
		Tools: []tool.Tool{calcTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, err = a.Generate(context.Background(), "calculate 2+2")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	lastCall := mock.LastCall()
	if lastCall == nil {
		t.Fatal("expected mock to record call")
	}
	if len(lastCall.Tools) != 1 || lastCall.Tools[0].Name != "calc" {
		t.Errorf("expected LLM request to contain calc tool, got %+v", lastCall.Tools)
	}
}

func TestAgent_Generate_ReActLoop(t *testing.T) {
	calcTool := tool.MustNew(tool.Config[string, string]{
		ID:          "calc",
		Description: "Perform calculations",
		Execute: func(_ context.Context, in string) (string, error) {
			if in == `{"expr":"2+2"}` {
				return "4", nil
			}
			return "unknown", nil
		},
	})

	callCount := 0
	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls: []llm.ToolCall{
						{
							ID:        "call-1",
							Name:      "calc",
							Arguments: `{"expr":"2+2"}`,
						},
					},
					Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			}
			return &llm.GenerateResponse{
				Text:         "The answer is 4",
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			}, nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:    "agent-calc",
		Model: mock,
		Tools: []tool.Tool{calcTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	res, err := a.Generate(context.Background(), "What is 2+2?")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls in ReAct loop, got %d", callCount)
	}
	if res.Text != "The answer is 4" {
		t.Errorf("expected final answer 'The answer is 4', got '%s'", res.Text)
	}
	if res.Usage.TotalTokens != 45 {
		t.Errorf("expected cumulative 45 total tokens, got %d", res.Usage.TotalTokens)
	}

	if len(res.Messages) != 4 {
		t.Fatalf("expected 4 conversation messages, got %d", len(res.Messages))
	}
	if res.Messages[0].Role != llm.RoleUser {
		t.Errorf("expected user message first, got %s", res.Messages[0].Role)
	}
	if res.Messages[1].Role != llm.RoleAssistant || len(res.Messages[1].ToolCalls) != 1 {
		t.Errorf("expected assistant tool call message second, got %+v", res.Messages[1])
	}
	if res.Messages[2].Role != llm.RoleTool || res.Messages[2].Content != "4" {
		t.Errorf("expected tool result message third with '4', got %+v", res.Messages[2])
	}
	if res.Messages[3].Role != llm.RoleAssistant || res.Messages[3].Content != "The answer is 4" {
		t.Errorf("expected final assistant reply fourth, got %+v", res.Messages[3])
	}
}

func TestAgent_Stream_ReActLoop_WithToolArgumentStreaming(t *testing.T) {
	weatherTool := tool.MustNew(tool.Config[string, string]{
		ID:          "get_weather",
		Description: "Fetch weather",
		Execute: func(_ context.Context, _ string) (string, error) {
			return `{"weather":"sunny"}`, nil
		},
	})

	stepIndex := 0
	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomStreamHandler(func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			stepIndex++
			if stepIndex == 1 {
				// Step 1: Model streams tool arguments in chunks (argument delta streaming)
				chunks := []*llm.StreamChunk{
					{
						ToolCalls: []llm.ToolCall{
							{ID: "call-1", Name: "get_weather", Arguments: `{"location":"`},
						},
					},
					{
						ToolCalls: []llm.ToolCall{
							{ID: "call-1", Arguments: `Tokyo"}`},
						},
						FinishReason: llm.FinishReasonTool,
					},
				}
				return llm.NewSliceStream(ctx, chunks), nil
			}

			// Step 2: Final response stream
			chunks := []*llm.StreamChunk{
				{Delta: "The weather "},
				{Delta: "is sunny!"},
			}
			return llm.NewSliceStream(ctx, chunks), nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:    "stream-agent",
		Model: mock,
		Tools: []tool.Tool{weatherTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := a.Stream(context.Background(), "Weather in Tokyo?")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var receivedDeltas strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv failed: %v", err)
		}
		receivedDeltas.WriteString(chunk.Delta)
	}

	if receivedDeltas.String() != "The weather is sunny!" {
		t.Errorf("unexpected streamed deltas: %s", receivedDeltas.String())
	}

	res, err := stream.Response()
	if err != nil {
		t.Fatalf("Response failed: %v", err)
	}

	if res.Text != "The weather is sunny!" {
		t.Errorf("unexpected final text: %s", res.Text)
	}

	if len(stream.ToolCalls()) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(stream.ToolCalls()))
	}
	if stream.ToolCalls()[0].Arguments != `{"location":"Tokyo"}` {
		t.Errorf("expected accumulated args '{\"location\":\"Tokyo\"}', got '%s'", stream.ToolCalls()[0].Arguments)
	}
}

func TestAgent_MaxSteps(t *testing.T) {
	repeatingTool := tool.MustNew(tool.Config[string, string]{
		ID:          "loop_tool",
		Description: "Loops",
		Execute: func(_ context.Context, _ string) (string, error) {
			return "done", nil
		},
	})

	callCount := 0
	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			callCount++
			return &llm.GenerateResponse{
				FinishReason: llm.FinishReasonTool,
				ToolCalls: []llm.ToolCall{
					{ID: "loop-call", Name: "loop_tool", Arguments: "{}"},
				},
			}, nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:       "looping-agent",
		Model:    mock,
		Tools:    []tool.Tool{repeatingTool},
		MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	res, err := a.Generate(context.Background(), "start loop")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected exactly 3 calls (maxSteps), got %d", callCount)
	}
	if res.FinishReason != llm.FinishReasonLength {
		t.Errorf("expected FinishReasonLength when max steps reached, got %s", res.FinishReason)
	}
}

func TestAgent_PerCallToolsOverride(t *testing.T) {
	perCallTool := tool.MustNew(tool.Config[string, string]{
		ID:          "dynamic_tool",
		Description: "A dynamic tool",
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})

	mock := llm.NewMockLLM("response")
	a, err := agent.New(agent.Config{
		ID:    "agent-no-default-tools",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, err = a.Generate(context.Background(), "call dynamic", agent.WithTools(perCallTool))
	if err != nil {
		t.Fatalf("Generate with tools failed: %v", err)
	}

	lastCall := mock.LastCall()
	if lastCall == nil || len(lastCall.Tools) != 1 || lastCall.Tools[0].Name != "dynamic_tool" {
		t.Errorf("expected dynamic_tool in last call, got %+v", lastCall)
	}
}
