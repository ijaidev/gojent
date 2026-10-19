package llm_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMockLLM_BasicResponses(t *testing.T) {
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
	mock := llm.NewMockLLM("canceled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := mock.Generate(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Error("expected error due to canceled context, got nil")
	}
}

func TestMessage_Constructors(t *testing.T) {
	user := llm.UserMessage("hello")
	if user.Role != llm.RoleUser || user.Text() != "hello" || len(user.Content) != 1 {
		t.Errorf("unexpected user message: %+v", user)
	}

	sys := llm.SystemMessage("instructions")
	if sys.Role != llm.RoleSystem || sys.Text() != "instructions" || len(sys.Content) != 1 {
		t.Errorf("unexpected system message: %+v", sys)
	}

	asst := llm.AssistantMessage("reply")
	if asst.Role != llm.RoleAssistant || asst.Text() != "reply" || len(asst.Content) != 1 {
		t.Errorf("unexpected assistant message: %+v", asst)
	}

	toolResult := llm.ToolResultMessage("call-123", `{"result": 42}`)
	if toolResult.Role != llm.RoleTool || toolResult.ToolCallID != "call-123" || toolResult.Text() != `{"result": 42}` {
		t.Errorf("unexpected tool result message: %+v", toolResult)
	}
}

func TestMessage_MultipartConstructors(t *testing.T) {
	imgBytes := []byte{0x89, 'P', 'N', 'G'}
	imgPart := llm.ImageFromBytes(imgBytes, "image/png")
	if imgPart.Type != llm.PartTypeImage || imgPart.MIMEType != "image/png" {
		t.Errorf("unexpected image part: %+v", imgPart)
	}

	userMsg := llm.UserMessageWithMedia("describe this", imgPart)
	if userMsg.Role != llm.RoleUser || len(userMsg.Content) != 2 {
		t.Fatalf("expected 2 parts in user message, got %d", len(userMsg.Content))
	}
	if userMsg.Content[0].Type != llm.PartTypeText || userMsg.Content[0].Text != "describe this" {
		t.Errorf("unexpected first part: %+v", userMsg.Content[0])
	}
	if userMsg.Content[1].Type != llm.PartTypeImage || len(userMsg.Content[1].Data) != 4 {
		t.Errorf("unexpected second part: %+v", userMsg.Content[1])
	}

	toolMsg := llm.ToolResultMessageWithParts("call-99", "screenshot", llm.TextPart(`{"ok":true}`), imgPart)
	if toolMsg.Role != llm.RoleTool || toolMsg.Name != "screenshot" || len(toolMsg.Content) != 2 {
		t.Errorf("unexpected tool message: %+v", toolMsg)
	}
}

func TestPart_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.png")
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	part, err := llm.ImageFromFile(filePath)
	if err != nil {
		t.Fatalf("ImageFromFile failed: %v", err)
	}
	if part.Type != llm.PartTypeImage || len(part.Data) != len(data) {
		t.Errorf("unexpected part: %+v", part)
	}
	if part.MIMEType != "image/png" {
		t.Errorf("expected mime image/png, got %s", part.MIMEType)
	}
}

func TestPart_Validate(t *testing.T) {
	// Valid text
	if err := llm.TextPart("hello").Validate(); err != nil {
		t.Errorf("unexpected error for valid text part: %v", err)
	}

	// Empty text
	if err := (llm.Part{Type: llm.PartTypeText}).Validate(); err == nil {
		t.Error("expected error for empty text part")
	}

	// Valid image bytes
	if err := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png").Validate(); err != nil {
		t.Errorf("unexpected error for valid image part: %v", err)
	}

	// Invalid empty media part
	if err := (llm.Part{Type: llm.PartTypeImage}).Validate(); err == nil {
		t.Error("expected error for empty image part")
	}

	// Invalid part type
	if err := (llm.Part{Type: "unknown"}).Validate(); err == nil {
		t.Error("expected error for unknown part type")
	}
}

func TestMessage_JSONUnmarshal(t *testing.T) {
	// String content JSON with thinking
	jsonStr := `{"role":"user","content":"hello world","thinking":"internal CoT"}`
	var msg1 llm.Message
	if err := json.Unmarshal([]byte(jsonStr), &msg1); err != nil {
		t.Fatalf("failed to unmarshal string content JSON: %v", err)
	}
	if msg1.Text() != "hello world" || len(msg1.Content) != 1 {
		t.Errorf("unexpected msg1 content: %+v", msg1)
	}
	if msg1.Thinking != "internal CoT" {
		t.Errorf("expected thinking 'internal CoT', got %q", msg1.Thinking)
	}

	// Array content JSON
	jsonArr := `{"role":"user","content":[{"type":"text","text":"part 1"},{"type":"image","url":"https://example.com/img.png"}]}`
	var msg2 llm.Message
	if err := json.Unmarshal([]byte(jsonArr), &msg2); err != nil {
		t.Fatalf("failed to unmarshal array content JSON: %v", err)
	}
	if len(msg2.Content) != 2 || msg2.Content[0].Text != "part 1" || msg2.Content[1].URL != "https://example.com/img.png" {
		t.Errorf("unexpected msg2: %+v", msg2)
	}
}

func TestThinkingConfig_CloneAndMessage(t *testing.T) {
	budget := 4096
	includeThoughts := true
	cfg := llm.ThinkingConfig{
		Level:           llm.ThinkingLevelMedium,
		BudgetTokens:    &budget,
		IncludeThoughts: &includeThoughts,
	}

	cloned := cfg.Clone()
	if cloned.Level != llm.ThinkingLevelMedium || *cloned.BudgetTokens != 4096 || *cloned.IncludeThoughts != true {
		t.Fatalf("unexpected cloned config: %+v", cloned)
	}

	// Mutating original should not affect clone
	*cfg.BudgetTokens = 2048
	if *cloned.BudgetTokens != 4096 {
		t.Errorf("expected clone to be independent, got budget %d", *cloned.BudgetTokens)
	}

	msg := llm.UserMessage("Analyze this").
		WithThinking("Pre-thought")

	if msg.Thinking != "Pre-thought" {
		t.Errorf("expected thinking 'Pre-thought', got %q", msg.Thinking)
	}
}

func TestResponseFormat_CloneAndMock(t *testing.T) {
	strict := true
	rf := &llm.ResponseFormat{
		Type:        llm.ResponseFormatJSONSchema,
		Name:        "TestSchema",
		Description: "A test schema",
		Schema:      map[string]any{"type": "object"},
		Strict:      &strict,
	}

	cloned := rf.Clone()
	if cloned.Type != llm.ResponseFormatJSONSchema || cloned.Name != "TestSchema" || *cloned.Strict != true {
		t.Fatalf("unexpected cloned format: %+v", cloned)
	}

	*rf.Strict = false
	if *cloned.Strict != true {
		t.Errorf("expected clone strict to remain true, got %v", *cloned.Strict)
	}

	mock := llm.NewMockLLM(`{"name":"Gojent"}`)
	req := llm.GenerateRequest{
		Messages:       []llm.Message{llm.UserMessage("Send JSON")},
		ResponseFormat: rf,
	}
	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Text != `{"name":"Gojent"}` {
		t.Errorf("unexpected response text: %s", res.Text)
	}
	if mock.LastCall().ResponseFormat == nil || mock.LastCall().ResponseFormat.Name != "TestSchema" {
		t.Errorf("expected mock to record response format, got %+v", mock.LastCall().ResponseFormat)
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
