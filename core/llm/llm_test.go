package llm_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMockLLM_SingleResponse(t *testing.T) {
	mock := llm.NewMockLLM("test response")
	if mock.ModelID() != "mock-llm" {
		t.Errorf("expected modelID mock-llm, got %s", mock.ModelID())
	}

	req := llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
		},
	}

	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "test response" {
		t.Errorf("expected 'test response', got '%s'", res.Text)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", mock.CallCount())
	}
	if mock.LastCall() == nil || len(mock.LastCall().Messages) != 1 {
		t.Errorf("expected last call to have 1 message")
	}
}

func TestMockLLM_SequentialResponses(t *testing.T) {
	mock := llm.NewMockLLM("first", "second")

	res1, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res1.Text != "first" {
		t.Errorf("expected 'first', got '%s'", res1.Text)
	}

	res2, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res2.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res2.Text)
	}

	// Should repeat the last response
	res3, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res3.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res3.Text)
	}
}

func TestMockLLM_NewMockllmAlias(t *testing.T) {
	mock := llm.NewMockllm("alias check")
	res, err := mock.Generate(context.Background(), llm.GenerateRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "alias check" {
		t.Errorf("expected 'alias check', got '%s'", res.Text)
	}
}

func TestMockLLM_RespectsContextCancellation(t *testing.T) {
	mock := llm.NewMockLLM("cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := mock.Generate(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Error("expected error due to cancelled context, got nil")
	}
}

func TestMessage_Constructors(t *testing.T) {
	user := llm.UserMessage("hello")
	if user.Role != llm.RoleUser || user.Content != "hello" {
		t.Errorf("unexpected user message: %+v", user)
	}

	sys := llm.SystemMessage("instructions")
	if sys.Role != llm.RoleSystem || sys.Content != "instructions" {
		t.Errorf("unexpected system message: %+v", sys)
	}

	asst := llm.AssistantMessage("reply")
	if asst.Role != llm.RoleAssistant || asst.Content != "reply" {
		t.Errorf("unexpected assistant message: %+v", asst)
	}

	toolResult := llm.ToolResultMessage("call-123", `{"result": 42}`)
	if toolResult.Role != llm.RoleTool || toolResult.ToolCallID != "call-123" || toolResult.Content != `{"result": 42}` {
		t.Errorf("unexpected tool result message: %+v", toolResult)
	}
}

func TestToolCall_UnmarshalArgs(t *testing.T) {
	tc := llm.ToolCall{
		ID:        "call-abc",
		Name:      "calc",
		Arguments: `{"a": 10, "b": 20}`,
	}

	var parsed struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	err := tc.UnmarshalArgs(&parsed)
	if err != nil {
		t.Fatalf("UnmarshalArgs failed: %v", err)
	}
	if parsed.A != 10 || parsed.B != 20 {
		t.Errorf("unexpected parsed values: %+v", parsed)
	}
}

func TestMockLLM_ToolCalls_GenerateAndStream(t *testing.T) {
	expectedCalls := []llm.ToolCall{
		{
			ID:        "call-1",
			Name:      "get_weather",
			Arguments: `{"location":"San Francisco"}`,
		},
	}

	mock := llm.NewMockLLM("tool calling...").ApplyOptions(
		llm.WithToolCalls(expectedCalls...),
	)

	req := llm.GenerateRequest{
		Messages: []llm.Message{llm.UserMessage("What is the weather?")},
		Tools: []llm.ToolDefinition{
			{Name: "get_weather", Description: "Fetches weather"},
		},
	}

	// 1. Generate test
	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.FinishReason != llm.FinishReasonTool {
		t.Errorf("expected finish reason tool_calls, got %s", res.FinishReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call-1" {
		t.Errorf("unexpected tool calls in response: %+v", res.ToolCalls)
	}

	// Verify Tools were recorded in request
	lastCall := mock.LastCall()
	if lastCall == nil || len(lastCall.Tools) != 1 || lastCall.Tools[0].Name != "get_weather" {
		t.Fatalf("expected recorded call to contain tools, got %+v", lastCall)
	}

	// 2. Stream test
	stream, err := mock.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	var foundToolCall bool
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if len(chunk.ToolCalls) > 0 {
			foundToolCall = true
			if chunk.FinishReason != llm.FinishReasonTool {
				t.Errorf("expected stream chunk finish reason tool_calls, got %s", chunk.FinishReason)
			}
			if chunk.ToolCalls[0].Name != "get_weather" {
				t.Errorf("unexpected tool call name: %s", chunk.ToolCalls[0].Name)
			}
		}
	}
	if !foundToolCall {
		t.Error("expected tool call to be emitted in stream")
	}

	// 3. Reset clears tool calls
	mock.Reset()
	if mock.CallCount() != 0 {
		t.Errorf("expected call count 0 after reset, got %d", mock.CallCount())
	}
	normalRes, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate after reset failed: %v", err)
	}
	if normalRes.FinishReason != llm.FinishReasonStop {
		t.Errorf("expected finish reason stop after reset, got %s", normalRes.FinishReason)
	}
}
