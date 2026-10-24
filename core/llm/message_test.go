package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

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

	userMsg := llm.UserMessageWithParts(llm.TextPart("describe this"), imgPart)
	if userMsg.Role != llm.RoleUser || len(userMsg.Content) != 2 {
		t.Fatalf("expected 2 parts in user message, got %d", len(userMsg.Content))
	}
	if userMsg.Content[0].Type != llm.PartTypeText || userMsg.Content[0].Text != "describe this" {
		t.Errorf("unexpected first part: %+v", userMsg.Content[0])
	}
	if userMsg.Content[1].Type != llm.PartTypeImage || len(userMsg.Content[1].Data) != 4 {
		t.Errorf("unexpected second part: %+v", userMsg.Content[1])
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

func TestMessage_WithThinking(t *testing.T) {
	msg := llm.UserMessage("Analyze this").
		WithThinking("Pre-thought")

	if msg.Thinking != "Pre-thought" {
		t.Errorf("expected thinking 'Pre-thought', got %q", msg.Thinking)
	}
}

func TestMessage_JSONRoundTripReplayState(t *testing.T) {
	original := llm.Message{
		Role:     llm.RoleAssistant,
		Content:  []llm.Part{llm.TextPart("")},
		Thinking: "reasoning",
		ToolCalls: []llm.ToolCall{
			{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`},
		},
		ProviderData: &llm.ProviderData{
			Provider: "test",
			Version:  2,
			Data:     []byte(`{"signature":"xyz"}`),
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(encoded) == 0 || jsonContains(encoded, `"content"`) {
		t.Errorf("empty content was not omitted: %s", encoded)
	}

	var decoded llm.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Content) != 0 {
		t.Errorf("expected no content parts, got %+v", decoded.Content)
	}
	if decoded.Thinking != "reasoning" {
		t.Errorf("Thinking = %q", decoded.Thinking)
	}
	if len(decoded.ToolCalls) != 1 || decoded.ToolCalls[0].ID != "call-1" || decoded.ToolCalls[0].Index != 0 {
		t.Errorf("ToolCalls = %+v", decoded.ToolCalls)
	}
	if decoded.ProviderData == nil || decoded.ProviderData.Provider != "test" || decoded.ProviderData.Version != 2 {
		t.Errorf("ProviderData = %+v", decoded.ProviderData)
	}
	if string(decoded.ProviderData.Data) != `{"signature":"xyz"}` {
		t.Errorf("ProviderData.Data = %s", decoded.ProviderData.Data)
	}
}

func TestProviderData_CloneIsIndependent(t *testing.T) {
	original := &llm.ProviderData{Provider: "test", Data: []byte(`{"a":1}`)}
	clone := original.Clone()
	clone.Data[0] = '['
	if original.Data[0] == '[' {
		t.Error("ProviderData.Clone aliases the original payload")
	}
	var nilData *llm.ProviderData
	if nilData.Clone() != nil {
		t.Error("nil ProviderData.Clone should return nil")
	}
}

func TestMessage_CloneIsIndependent(t *testing.T) {
	original := llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.Part{llm.TextPart("hello")},
		Data:    []byte(`{"a":1}`),
		ProviderData: &llm.ProviderData{
			Provider: "test",
			Data:     []byte(`{"b":2}`),
		},
	}
	clone := original.Clone()
	clone.Content[0].Text = "changed"
	clone.Data[0] = '['
	clone.ProviderData.Data[0] = '['
	if original.Content[0].Text != "hello" || original.Data[0] == '[' || original.ProviderData.Data[0] == '[' {
		t.Error("Message.Clone shares mutable state with the original")
	}
}

func jsonContains(data []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(data); i++ {
		if string(data[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
