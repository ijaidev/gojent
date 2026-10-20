package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/mockllm"
	"github.com/ijaidev/gojent/stores/inmemory"
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
	mock := mockllm.New("I can help you with recipes!")

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
	if lastCall.Messages[0].Role != llm.RoleSystem || lastCall.Messages[0].Text() != "You are a professional chef." {
		t.Errorf("expected system instructions, got %+v", lastCall.Messages[0])
	}
	if lastCall.Messages[1].Role != llm.RoleUser || lastCall.Messages[1].Text() != "What can I cook with tomatoes?" {
		t.Errorf("expected user message, got %+v", lastCall.Messages[1])
	}

	// Verify the returned conversation history
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 conversation messages in response, got %d", len(res.Messages))
	}
	if res.Messages[2].Role != llm.RoleAssistant || res.Messages[2].Text() != "I can help you with recipes!" {
		t.Errorf("expected assistant response in messages, got %+v", res.Messages[2])
	}
}

func TestAgent_SwapModel(t *testing.T) {
	mock1 := mockllm.New("Response from Model 1")
	mock2 := mockllm.New("Response from Model 2")

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
	defaultMock := mockllm.New("Default response")
	overrideMock := mockllm.New("Overridden response")

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

	mock := mockllm.New("ok")
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
	var typedNil *tool.BaseTool[string, string]
	err = a.AddTool(typedNil)
	if !errors.Is(err, agent.ErrToolNil) {
		t.Errorf("expected ErrToolNil for typed-nil tool, got %v", err)
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

	mock := mockllm.New("computed")
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
			if in == "2+2" {
				return "4", nil
			}
			return "unknown", nil
		},
	})

	callCount := 0
	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls: []llm.ToolCall{
						{
							ID:        "call-1",
							Name:      "calc",
							Arguments: `"2+2"`,
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
	if res.Messages[2].Role != llm.RoleTool || res.Messages[2].Text() != "4" {
		t.Errorf("expected tool result message third with '4', got %+v", res.Messages[2])
	}
	if res.Messages[3].Role != llm.RoleAssistant || res.Messages[3].Text() != "The answer is 4" {
		t.Errorf("expected final assistant reply fourth, got %+v", res.Messages[3])
	}
}

func TestAgent_Stream_ReActLoop_WithToolArgumentStreaming(t *testing.T) {
	weatherTool := tool.MustNew(tool.Config[struct {
		Location string `json:"location"`
	}, string]{
		ID:          "get_weather",
		Description: "Fetch weather",
		Execute: func(_ context.Context, _ struct {
			Location string `json:"location"`
		}) (string, error) {
			return `{"weather":"sunny"}`, nil
		},
	})

	stepIndex := 0
	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomStreamHandler(func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
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
	repeatingTool := tool.MustNew(tool.Config[struct{}, string]{
		ID:          "loop_tool",
		Description: "Loops",
		Execute: func(_ context.Context, _ struct{}) (string, error) {
			return "done", nil
		},
	})

	callCount := 0
	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
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

	mock := mockllm.New("response")
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

func TestAgent_WithStorage_ConversationMemory(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	responses := []string{
		"Got it, your favorite color is blue.",
		"Your favorite color is blue!",
	}
	mock := mockllm.New(responses...)

	a, err := agent.New(agent.Config{
		ID:           "memory-agent",
		Instructions: "Remember facts.",
		Model:        mock,
		Storage:      store,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	threadID := "user-thread-42"

	// Turn 1
	res1, err := a.Generate(ctx, "My favorite color is blue.", agent.WithThreadID(threadID), agent.WithResourceID("user-1"))
	if err != nil {
		t.Fatalf("Turn 1 Generate failed: %v", err)
	}
	if res1.Text != responses[0] {
		t.Errorf("Turn 1 unexpected response: %s", res1.Text)
	}

	// Verify thread was automatically saved
	th, err := store.GetThreadByID(ctx, threadID)
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if th.ResourceID != "user-1" {
		t.Errorf("expected thread resourceID 'user-1', got %q", th.ResourceID)
	}

	// Verify messages saved after Turn 1 (user message + assistant message)
	msgs1, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: threadID})
	if err != nil {
		t.Fatalf("ListMessages Turn 1 failed: %v", err)
	}
	if len(msgs1) != 2 {
		t.Fatalf("expected 2 messages in store after Turn 1, got %d", len(msgs1))
	}

	// Turn 2
	res2, err := a.Generate(ctx, "What is my favorite color?", agent.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("Turn 2 Generate failed: %v", err)
	}
	if res2.Text != responses[1] {
		t.Errorf("Turn 2 unexpected response: %s", res2.Text)
	}

	// Verify that the Turn 2 request to LLM contained history from Turn 1!
	lastCall := mock.LastCall()
	if lastCall == nil {
		t.Fatalf("expected lastCall recorded on mock LLM")
	}
	// Expected messages: Instructions (system), Turn 1 User, Turn 1 Assistant, Turn 2 User
	if len(lastCall.Messages) != 4 {
		t.Fatalf("expected 4 messages sent to LLM in Turn 2, got %d: %+v", len(lastCall.Messages), lastCall.Messages)
	}
	if lastCall.Messages[1].Text() != "My favorite color is blue." {
		t.Errorf("expected Turn 1 history in message 1, got: %s", lastCall.Messages[1].Content)
	}
	if lastCall.Messages[2].Text() != responses[0] {
		t.Errorf("expected Turn 1 response in message 2, got: %s", lastCall.Messages[2].Content)
	}
	if lastCall.Messages[3].Text() != "What is my favorite color?" {
		t.Errorf("expected Turn 2 user prompt in message 3, got: %s", lastCall.Messages[3].Content)
	}

	// Verify that store now contains all 4 messages
	allMsgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: threadID})
	if err != nil {
		t.Fatalf("ListMessages after Turn 2 failed: %v", err)
	}
	if len(allMsgs) != 4 {
		t.Errorf("expected 4 messages total in store, got %d", len(allMsgs))
	}
}

func TestAgent_StorageGetterAndSetter(t *testing.T) {
	mock := mockllm.New("hello")
	store1 := inmemory.New()
	store2 := inmemory.New()

	a, err := agent.New(agent.Config{
		ID:      "test-agent",
		Model:   mock,
		Storage: store1,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	if a.Storage() != store1 {
		t.Errorf("expected store1, got %v", a.Storage())
	}

	a.SetStorage(store2)
	if a.Storage() != store2 {
		t.Errorf("expected store2 after SetStorage, got %v", a.Storage())
	}
}

func TestAgent_Stream_WithStorage(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	mock := mockllm.New("Streamed response")
	a, err := agent.New(agent.Config{
		ID:      "streaming-agent",
		Model:   mock,
		Storage: store,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	threadID := "stream-thread-1"
	stream, err := a.Stream(ctx, "Hello streaming!", agent.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if res.Text != "Streamed response" {
		t.Errorf("unexpected streamed text: %s", res.Text)
	}

	// Verify messages were saved in store
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: threadID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages in store (user + assistant), got %d", len(msgs))
	}
	if msgs[0].Text() != "Hello streaming!" || msgs[1].Text() != "Streamed response" {
		t.Errorf("unexpected saved contents: %+v", msgs)
	}
}

func TestAgent_WithStorage_MaxSteps_NoDuplicateMessages(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	defer func() { _ = store.Close() }()

	toolCalled := false
	testTool := tool.MustNew(tool.Config[string, string]{
		ID:          "step_tool",
		Description: "A tool that does work",
		Execute: func(_ context.Context, _ string) (string, error) {
			toolCalled = true
			return "done", nil
		},
	})

	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
			return &llm.GenerateResponse{
				FinishReason: llm.FinishReasonTool,
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "step_tool", Arguments: `"test"`},
				},
			}, nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:       "max-steps-agent",
		Model:    mock,
		Storage:  store,
		Tools:    []tool.Tool{testTool},
		MaxSteps: 1,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	threadID := "max-steps-thread"
	resp, err := a.Generate(ctx, "run tool once", agent.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if !toolCalled {
		t.Fatalf("expected tool to be called")
	}
	if resp.FinishReason != llm.FinishReasonLength {
		t.Errorf("expected FinishReasonLength, got %v", resp.FinishReason)
	}

	// Verify messages in store: exactly 3 (User, Assistant, Tool). No duplicate!
	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: threadID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected exactly 3 messages in store, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser || msgs[1].Role != llm.RoleAssistant || msgs[2].Role != llm.RoleTool {
		t.Errorf("unexpected message roles: %v, %v, %v", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
}

func TestAgent_WithMedia(t *testing.T) {
	ctx := context.Background()
	mock := mockllm.New("I see an image of a red apple.")

	a, err := agent.New(agent.Config{
		ID:    "vision-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	imgPart := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	res, err := a.Generate(ctx, "What is this?", agent.WithMedia(imgPart))
	if err != nil {
		t.Fatalf("Generate with media failed: %v", err)
	}
	if res.Text != "I see an image of a red apple." {
		t.Errorf("unexpected response: %s", res.Text)
	}

	lastCall := mock.LastCall()
	if lastCall == nil || len(lastCall.Messages) != 1 {
		t.Fatalf("expected 1 recorded message, got %+v", lastCall)
	}
	userMsg := lastCall.Messages[0]
	if len(userMsg.Content) != 2 {
		t.Fatalf("expected 2 parts in user message, got %d", len(userMsg.Content))
	}
	if userMsg.Content[0].Text != "What is this?" || userMsg.Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected parts in user message: %+v", userMsg.Content)
	}
}

func TestAgent_WithMediaOnly(t *testing.T) {
	ctx := context.Background()
	mock := mockllm.New("I see an image.")

	a, err := agent.New(agent.Config{
		ID:    "vision-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	imgPart := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	if _, err := a.Generate(ctx, "", agent.WithMedia(imgPart)); err != nil {
		t.Fatalf("Generate with media-only prompt failed: %v", err)
	}

	lastCall := mock.LastCall()
	if lastCall == nil || len(lastCall.Messages) != 1 {
		t.Fatalf("expected 1 recorded message, got %+v", lastCall)
	}
	userMsg := lastCall.Messages[0]
	if userMsg.Role != llm.RoleUser || len(userMsg.Content) != 1 {
		t.Fatalf("expected one user message with one part, got %+v", userMsg)
	}
	if userMsg.Content[0].Type != llm.PartTypeImage {
		t.Errorf("expected image part, got %+v", userMsg.Content[0])
	}
}

func TestAgent_MultipartToolResult(t *testing.T) {
	ctx := context.Background()
	imgPart := llm.ImageFromBytes([]byte{10, 20, 30}, "image/png")

	screenshotTool := tool.MustNew(tool.Config[string, tool.Result]{
		ID:          "take_screenshot",
		Description: "Captures display",
		Execute: func(_ context.Context, _ string) (tool.Result, error) {
			return tool.NewImageResult(`{"status":"captured"}`, imgPart), nil
		},
	})

	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
			if len(req.Messages) == 1 {
				return &llm.GenerateResponse{
					FinishReason: llm.FinishReasonTool,
					ToolCalls: []llm.ToolCall{
						{ID: "call_snap", Name: "take_screenshot", Arguments: `""`},
					},
				}, nil
			}
			lastMsg := req.Messages[len(req.Messages)-1]
			if lastMsg.Role != llm.RoleTool {
				return nil, fmt.Errorf("expected role tool, got %v", lastMsg.Role)
			}
			if len(lastMsg.Content) != 2 || lastMsg.Content[1].Type != llm.PartTypeImage {
				return nil, fmt.Errorf("expected 2 parts in tool result, got %+v", lastMsg.Content)
			}
			return &llm.GenerateResponse{
				Text:         "The screen shows a blue sky.",
				FinishReason: llm.FinishReasonStop,
			}, nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:    "screenshot-agent",
		Model: mock,
		Tools: []tool.Tool{screenshotTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	res, err := a.Generate(ctx, "Take a screenshot and describe it")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Text != "The screen shows a blue sky." {
		t.Errorf("unexpected response: %s", res.Text)
	}
}

func TestAgent_ThinkingConfigurationAndOverride(t *testing.T) {
	ctx := context.Background()

	var recordedReq llm.GenerateRequest
	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
			recordedReq = req
			return &llm.GenerateResponse{
				Text:         "42",
				Thinking:     "Thinking steps...",
				FinishReason: llm.FinishReasonStop,
				Usage: llm.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					ReasoningTokens:  15,
				},
			}, nil
		}),
	)

	defaultThinking := &llm.ThinkingConfig{Level: llm.ThinkingLevelLow}
	a, err := agent.New(agent.Config{
		ID:       "thinking-agent",
		Model:    mock,
		Thinking: defaultThinking,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// 1. Generation with agent-level default thinking
	res1, err := a.Generate(ctx, "Calculate meaning")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res1.Thinking != "Thinking steps..." {
		t.Errorf("expected response thinking 'Thinking steps...', got %q", res1.Thinking)
	}
	if recordedReq.Thinking == nil || recordedReq.Thinking.Level != llm.ThinkingLevelLow {
		t.Errorf("expected request thinking level low, got %+v", recordedReq.Thinking)
	}
	if res1.Usage.ReasoningTokens != 15 {
		t.Errorf("expected reasoning tokens 15, got %d", res1.Usage.ReasoningTokens)
	}

	// 2. Generation with WithThinkingLevel override (generate message config)
	_, err = a.Generate(ctx, "Calculate meaning", agent.WithThinkingLevel(llm.ThinkingLevelMedium))
	if err != nil {
		t.Fatalf("Generate with option failed: %v", err)
	}
	if recordedReq.Thinking == nil || recordedReq.Thinking.Level != llm.ThinkingLevelMedium {
		t.Errorf("expected request thinking level medium, got %+v", recordedReq.Thinking)
	}

	// 3. Generation with WithThinkingBudget override
	_, err = a.Generate(ctx, "Hard problem", agent.WithThinkingBudget(2048))
	if err != nil {
		t.Fatalf("Generate with budget option failed: %v", err)
	}
	if recordedReq.Thinking == nil || recordedReq.Thinking.BudgetTokens == nil || *recordedReq.Thinking.BudgetTokens != 2048 {
		t.Errorf("expected request thinking budget 2048, got %+v", recordedReq.Thinking)
	}

	// 4. Generation with full WithThinking config override
	budget := 4096
	_, err = a.Generate(ctx, "Deep thinking", agent.WithThinking(llm.ThinkingConfig{
		Level:        llm.ThinkingLevelHigh,
		BudgetTokens: &budget,
	}))
	if err != nil {
		t.Fatalf("Generate with full thinking option failed: %v", err)
	}
	if recordedReq.Thinking == nil || recordedReq.Thinking.Level != llm.ThinkingLevelHigh || recordedReq.Thinking.BudgetTokens == nil || *recordedReq.Thinking.BudgetTokens != 4096 {
		t.Errorf("expected request thinking level high and budget 4096, got %+v", recordedReq.Thinking)
	}
}

func TestAgent_StreamThinking(t *testing.T) {
	ctx := context.Background()

	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomStreamHandler(func(ctx context.Context, _ llm.GenerateRequest) (llm.Stream, error) {
			chunks := []*llm.StreamChunk{
				{ThinkingDelta: "Step 1: Analyzing... "},
				{ThinkingDelta: "Step 2: Concluding..."},
				{Delta: "The answer is "},
				{Delta: "42.", FinishReason: llm.FinishReasonStop, Usage: &llm.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					ReasoningTokens:  20,
				}},
			}
			return llm.NewSliceStream(ctx, chunks), nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:    "stream-thinking-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	stream, err := a.Stream(ctx, "Solve puzzle", agent.WithThinkingLevel(llm.ThinkingLevelHigh))
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	res, err := stream.Drain()
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}

	if stream.Thinking() != "Step 1: Analyzing... Step 2: Concluding..." {
		t.Errorf("unexpected stream.Thinking(): %q", stream.Thinking())
	}
	if stream.Text() != "The answer is 42." {
		t.Errorf("unexpected stream.Text(): %q", stream.Text())
	}
	if res.Thinking != "Step 1: Analyzing... Step 2: Concluding..." {
		t.Errorf("unexpected res.Thinking: %q", res.Thinking)
	}
	if res.Text != "The answer is 42." {
		t.Errorf("unexpected res.Text: %q", res.Text)
	}
	if res.Usage.ReasoningTokens != 20 {
		t.Errorf("expected reasoning tokens 20, got %d", res.Usage.ReasoningTokens)
	}
}

func TestAgent_ToolDefinitionsAreDeterministicallyOrdered(t *testing.T) {
	var names []string
	mock := mockllm.New().ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
			names = names[:0]
			for _, def := range req.Tools {
				names = append(names, def.Name)
			}
			return &llm.GenerateResponse{Text: "ok", FinishReason: llm.FinishReasonStop}, nil
		}),
	)

	// Registered deliberately out of order; map iteration randomizes further.
	ids := []string{"zulu", "alpha", "mike", "delta", "echo", "bravo", "yankee", "charlie"}
	regs := make([]tool.Tool, 0, len(ids))
	for _, id := range ids {
		regs = append(regs, tool.MustNew(tool.Config[string, string]{
			ID:          id,
			Description: "test tool",
			Execute:     func(_ context.Context, in string) (string, error) { return in, nil },
		}))
	}

	a, err := agent.New(agent.Config{ID: "ordering-agent", Model: mock, Tools: regs})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	if _, err := a.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	const want = "alpha,bravo,charlie,delta,echo,mike,yankee,zulu"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("expected deterministically ordered tools\n got: %s\nwant: %s", got, want)
	}
}

func TestAgent_WithMediaDoesNotMutateInputMessages(t *testing.T) {
	mock := mockllm.New("seen")
	a, err := agent.New(agent.Config{ID: "media-agent", Model: mock})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	msgs := []llm.Message{llm.UserMessage("What is this?")}
	img := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")

	if _, err := a.GenerateMessages(context.Background(), msgs, agent.WithMedia(img)); err != nil {
		t.Fatalf("GenerateMessages failed: %v", err)
	}

	if len(msgs[0].Content) != 1 {
		t.Errorf("WithMedia mutated the caller's message: got %d parts, want 1", len(msgs[0].Content))
	}
}
