package agent_test

import (
	"context"
	"errors"
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

func TestAgent_Generate_ReceivesToolCalls(t *testing.T) {
	expectedCalls := []llm.ToolCall{
		{
			ID:        "call-xyz",
			Name:      "search",
			Arguments: `{"query":"golang"}`,
		},
	}

	mock := llm.NewMockLLM().ApplyOptions(
		llm.WithToolCalls(expectedCalls...),
	)

	a, err := agent.New(agent.Config{
		ID:    "agent-search",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	res, err := a.Generate(context.Background(), "search for golang")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if res.FinishReason != llm.FinishReasonTool {
		t.Errorf("expected finish reason tool_calls, got %s", res.FinishReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call-xyz" {
		t.Errorf("expected tool call call-xyz, got %+v", res.ToolCalls)
	}

	// Verify the assistant conversation message includes tool calls
	lastMsg := res.Messages[len(res.Messages)-1]
	if lastMsg.Role != llm.RoleAssistant {
		t.Errorf("expected assistant message, got %s", lastMsg.Role)
	}
	if len(lastMsg.ToolCalls) != 1 || lastMsg.ToolCalls[0].Name != "search" {
		t.Errorf("expected assistant message to contain tool call, got %+v", lastMsg.ToolCalls)
	}
}

func TestAgent_Stream_ReceivesToolCalls(t *testing.T) {
	expectedCalls := []llm.ToolCall{
		{
			ID:        "stream-call-1",
			Name:      "fetch_data",
			Arguments: `{"id":1}`,
		},
	}

	mock := llm.NewMockLLM("stream chunk").ApplyOptions(
		llm.WithToolCalls(expectedCalls...),
	)

	a, err := agent.New(agent.Config{
		ID:    "stream-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := a.Stream(context.Background(), "stream with tool")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}

	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "stream-call-1" {
		t.Errorf("unexpected tool calls in drained response: %+v", res.ToolCalls)
	}
	if len(stream.ToolCalls()) != 1 {
		t.Errorf("expected stream.ToolCalls() to report 1 call, got %d", len(stream.ToolCalls()))
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
