package llm_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMessage_Constructors(t *testing.T) {
	tests := []struct {
		name string
		msg  llm.Message
		role llm.Role
		text string
	}{
		{"user", llm.UserMessage("hello"), llm.RoleUser, "hello"},
		{"system", llm.SystemMessage("be brief"), llm.RoleSystem, "be brief"},
		{"developer", llm.DeveloperMessage("use tabs"), llm.RoleDeveloper, "use tabs"},
		{"assistant", llm.AssistantMessage("hi"), llm.RoleAssistant, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.msg.Role != tt.role {
				t.Errorf("role = %q, want %q", tt.msg.Role, tt.role)
			}
			if got := tt.msg.Text(); got != tt.text {
				t.Errorf("text = %q, want %q", got, tt.text)
			}
			if err := tt.msg.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestMessage_MultipartConstructors(t *testing.T) {
	msg := llm.AssistantMessageWithParts(
		llm.NewReasoningPart("thinking"),
		llm.NewTextPart("answer"),
	)
	if len(msg.Content) != 2 {
		t.Fatalf("content = %d, want 2", len(msg.Content))
	}
	if _, ok := msg.Content[0].(*llm.ReasoningPart); !ok {
		t.Errorf("content[0] = %T, want reasoning", msg.Content[0])
	}
	if got := msg.Text(); got != "answer" {
		t.Errorf("text = %q", got)
	}
}

func TestMessage_ValidatePlacement(t *testing.T) {
	call := llm.NewToolCallPart(llm.ToolCall{CallID: "call-1", Name: "lookup"})
	result := llm.NewToolResultPart(llm.ToolResult{CallID: "call-1", Content: []llm.Part{llm.NewTextPart("ok")}})

	tests := []struct {
		name string
		msg  llm.Message
		want error
	}{
		{
			name: "valid assistant",
			msg:  llm.AssistantMessageWithParts(llm.NewReasoningPart("r"), llm.NewTextPart("t"), call),
		},
		{
			name: "valid tool result",
			msg:  llm.UserMessageWithParts(result),
		},
		{
			name: "invalid role",
			msg:  llm.Message{Role: "narrator", Content: []llm.Part{llm.NewTextPart("x")}},
			want: llm.ErrInvalidMessage,
		},
		{
			name: "empty content",
			msg:  llm.Message{Role: llm.RoleUser},
			want: llm.ErrInvalidMessage,
		},
		{
			name: "empty text",
			msg:  llm.Message{Role: llm.RoleUser, Content: []llm.Part{llm.NewTextPart("")}},
			want: llm.ErrInvalidPart,
		},
		{
			name: "tool call in user message",
			msg:  llm.UserMessageWithParts(call),
			want: llm.ErrInvalidPart,
		},
		{
			name: "tool result in assistant message",
			msg:  llm.AssistantMessageWithParts(result),
			want: llm.ErrInvalidPart,
		},
		{
			name: "reasoning in user message",
			msg:  llm.UserMessageWithParts(llm.NewReasoningPart("r")),
			want: llm.ErrInvalidPart,
		},
		{
			name: "refusal in user message",
			msg:  llm.UserMessageWithParts(llm.NewRefusalPart("no")),
			want: llm.ErrInvalidPart,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateTranscript(t *testing.T) {
	call := func(id string, kind llm.ToolKind) *llm.ToolCallPart {
		return llm.NewToolCallPart(llm.ToolCall{CallID: id, Name: "tool", Kind: kind})
	}
	result := func(id string) *llm.ToolResultPart {
		return llm.NewToolResultPart(llm.ToolResult{
			CallID:  id,
			Name:    "tool",
			Kind:    llm.ToolKindFunction,
			Content: []llm.Part{llm.NewTextPart("ok")},
		})
	}

	tests := []struct {
		name     string
		messages []llm.Message
		want     error
	}{
		{
			name: "paired",
			messages: []llm.Message{
				llm.UserMessage("hi"),
				llm.AssistantMessageWithParts(call("a", llm.ToolKindFunction), call("b", llm.ToolKindFunction)),
				llm.UserMessageWithParts(result("a"), result("b")),
				llm.AssistantMessage("done"),
			},
		},
		{
			name: "server call needs no result",
			messages: []llm.Message{
				llm.UserMessage("search"),
				llm.AssistantMessageWithParts(call("s", llm.ToolKindServer)),
				llm.AssistantMessage("done"),
			},
		},
		{
			name: "trailing calls allowed",
			messages: []llm.Message{
				llm.UserMessage("hi"),
				llm.AssistantMessageWithParts(call("a", llm.ToolKindFunction)),
			},
		},
		{
			name: "missing result",
			messages: []llm.Message{
				llm.UserMessage("hi"),
				llm.AssistantMessageWithParts(call("a", llm.ToolKindFunction)),
				llm.UserMessageWithParts(result("b")),
			},
			want: llm.ErrInvalidMessage,
		},
		{
			name: "result without call",
			messages: []llm.Message{
				llm.UserMessageWithParts(result("a")),
			},
			want: llm.ErrInvalidMessage,
		},
		{
			name: "duplicate call id",
			messages: []llm.Message{
				llm.UserMessage("hi"),
				llm.AssistantMessageWithParts(call("a", llm.ToolKindFunction)),
				llm.UserMessageWithParts(result("a")),
				llm.AssistantMessageWithParts(call("a", llm.ToolKindFunction)),
				llm.UserMessageWithParts(result("a")),
			},
			want: llm.ErrInvalidMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := llm.ValidateTranscript(tt.messages)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("ValidateTranscript: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateTranscript = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestMessage_JSONRoundTrip(t *testing.T) {
	calls := llm.NewToolCallPart(llm.ToolCall{
		ItemID: "item-1",
		CallID: "call-1",
		Name:   "lookup",
		Kind:   llm.ToolKindFunction,
		Input:  json.RawMessage(`{"q":"go"}`),
	})
	if err := calls.Set("google", map[string]string{"signature": "sig"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	result := llm.NewToolResultPart(llm.ToolResult{
		CallID:     "call-1",
		Name:       "lookup",
		Kind:       llm.ToolKindFunction,
		Status:     llm.ToolStatusCompleted,
		Content:    []llm.Part{llm.NewTextPart("found"), llm.FileFromBytes([]byte("png"), "image/png")},
		Structured: json.RawMessage(`{"ok":true}`),
	})
	message := llm.AssistantMessageWithParts(
		llm.NewReasoningPart("plan"),
		llm.NewTextPart("calling"),
		calls,
	)
	message.ItemID = "msg-1"
	message.Status = llm.MessageStatusCompleted
	message.Phase = "final_answer"
	message.Name = "assistant"
	message.Provider = make(llm.ProviderData)
	if err := message.Provider.Set("openai", map[string]string{"response_id": "resp-1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded llm.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate decoded: %v", err)
	}
	if len(decoded.Content) != 3 {
		t.Fatalf("content = %d, want 3", len(decoded.Content))
	}
	gotCall, ok := decoded.Content[2].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("content[2] = %T", decoded.Content[2])
	}
	if gotCall.ItemID != "item-1" || gotCall.CallID != "call-1" || string(gotCall.Input) != `{"q":"go"}` {
		t.Errorf("call = %+v", gotCall.ToolCall)
	}
	var signature map[string]string
	if err := gotCall.Get("google", &signature); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if signature["signature"] != "sig" {
		t.Errorf("signature = %v", signature)
	}
	if decoded.ItemID != "msg-1" || decoded.Phase != "final_answer" {
		t.Errorf("message metadata = %+v", decoded)
	}

	resultMessage := llm.UserMessageWithParts(result)
	encoded, err = json.Marshal(resultMessage)
	if err != nil {
		t.Fatalf("Marshal result: %v", err)
	}
	var decodedResult llm.Message
	if err := json.Unmarshal(encoded, &decodedResult); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if err := decodedResult.Validate(); err != nil {
		t.Fatalf("Validate result: %v", err)
	}
	gotResult := decodedResult.ToolResults()[0]
	if gotResult.CallID != "call-1" || string(gotResult.Structured) != `{"ok":true}` {
		t.Errorf("result = %+v", gotResult)
	}
	if len(gotResult.Content) != 2 {
		t.Fatalf("result content = %d", len(gotResult.Content))
	}
	file, ok := gotResult.Content[1].(*llm.FilePart)
	if !ok {
		t.Fatalf("result content[1] = %T", gotResult.Content[1])
	}
	if file.MIMEType != "image/png" || string(file.Data) != "png" {
		t.Errorf("file = %+v", file)
	}
}

func TestMessage_JSONUnknownPartType(t *testing.T) {
	var message llm.Message
	err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"hologram","text":"hi"}]}`), &message)
	if !errors.Is(err, llm.ErrUnknownPartType) {
		t.Fatalf("Unmarshal = %v, want ErrUnknownPartType", err)
	}
}

func TestMessage_CloneIsIndependent(t *testing.T) {
	original := llm.AssistantMessageWithParts(
		llm.NewTextPart("hello"),
		llm.NewToolCallPart(llm.ToolCall{
			CallID: "call-1",
			Name:   "lookup",
			Input:  json.RawMessage(`{"q":"go"}`),
		}),
	)
	if err := original.Content[1].(*llm.ToolCallPart).Set("google", map[string]string{"sig": "a"}); err != nil {
		t.Fatal(err)
	}

	clone := original.Clone()
	clone.Content[0].(*llm.TextPart).Text = "changed"
	cloneCall := clone.Content[1].(*llm.ToolCallPart)
	cloneCall.Input[0] = '['
	cloneCall.Provider["google"] = json.RawMessage(`{"sig":"b"}`)

	if got := original.Content[0].(*llm.TextPart).Text; got != "hello" {
		t.Errorf("original text = %q", got)
	}
	originalCall := original.Content[1].(*llm.ToolCallPart)
	if string(originalCall.Input) != `{"q":"go"}` {
		t.Errorf("original input = %s", originalCall.Input)
	}
	var signature map[string]string
	if err := originalCall.Get("google", &signature); err != nil {
		t.Fatal(err)
	}
	if signature["sig"] != "a" {
		t.Errorf("original signature = %v", signature)
	}
}

func TestProviderData_NilSafe(t *testing.T) {
	var data llm.ProviderData
	if _, ok := data.Raw("google"); ok {
		t.Error("Raw on nil returned a value")
	}
	if err := data.Get("google", &map[string]string{}); !errors.Is(err, llm.ErrNoProviderData) {
		t.Errorf("Get on nil = %v, want ErrNoProviderData", err)
	}
	if err := data.Set("google", "x"); !errors.Is(err, llm.ErrNoProviderData) {
		t.Errorf("Set on nil = %v, want ErrNoProviderData", err)
	}
	if clone := data.Clone(); clone != nil {
		t.Errorf("Clone of nil = %v", clone)
	}
}

func TestMarshalMessage_FormatVersion(t *testing.T) {
	encoded, err := llm.MarshalMessage(llm.UserMessage("hello"))
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	var header map[string]any
	if err := json.Unmarshal(encoded, &header); err != nil {
		t.Fatal(err)
	}
	if header["format_version"] != float64(llm.FormatVersion) {
		t.Errorf("format_version = %v", header["format_version"])
	}
	decoded, err := llm.UnmarshalMessage(encoded)
	if err != nil {
		t.Fatalf("UnmarshalMessage: %v", err)
	}
	if decoded.Text() != "hello" {
		t.Errorf("text = %q", decoded.Text())
	}

	if _, err := llm.UnmarshalMessage([]byte(`{"format_version":1,"role":"user","content":[{"type":"text","text":"hi"}]}`)); !errors.Is(err, llm.ErrUnsupportedFormatVersion) {
		t.Errorf("version 1 = %v, want ErrUnsupportedFormatVersion", err)
	}
	if _, err := llm.UnmarshalMessage([]byte(`{"role":"user"}`)); !errors.Is(err, llm.ErrUnsupportedFormatVersion) {
		t.Errorf("missing version = %v, want ErrUnsupportedFormatVersion", err)
	}
}
