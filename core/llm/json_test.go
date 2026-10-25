package llm_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestPartJSON_GoldenShapes(t *testing.T) {
	tests := []struct {
		name string
		part llm.Part
		want string
	}{
		{
			name: "text",
			part: llm.NewTextPart("hi"),
			want: `{"type":"text","text":"hi"}`,
		},
		{
			name: "reasoning",
			part: func() llm.Part {
				part := llm.NewReasoningPart("why")
				part.Summary = []string{"step"}
				part.Redacted = true
				return part
			}(),
			want: `{"type":"reasoning","text":"why","summary":["step"],"redacted":true}`,
		},
		{
			name: "file",
			part: llm.FileFromBytes([]byte("png"), "image/png"),
			want: `{"type":"file","mime_type":"image/png","data":"cG5n"}`,
		},
		{
			name: "tool call",
			part: llm.NewToolCallPart(llm.ToolCall{
				ItemID: "item-1",
				CallID: "call-1",
				Name:   "lookup",
				Kind:   llm.ToolKindFunction,
				Input:  json.RawMessage(`{"q":"go"}`),
			}),
			want: `{"type":"tool_call","item_id":"item-1","call_id":"call-1","name":"lookup","kind":"function","input":{"q":"go"}}`,
		},
		{
			name: "refusal",
			part: llm.NewRefusalPart("no"),
			want: `{"type":"refusal","text":"no"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.part)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(encoded) != tt.want {
				t.Errorf("JSON = %s, want %s", encoded, tt.want)
			}
			decoded, err := decodeOne(t, encoded)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if decoded.PartType() != tt.part.PartType() {
				t.Errorf("type = %q, want %q", decoded.PartType(), tt.part.PartType())
			}
		})
	}
}

func decodeOne(t *testing.T, encoded []byte) (llm.Part, error) {
	t.Helper()
	message := llm.AssistantMessageWithParts()
	message.Role = llm.RoleUser
	raw := []byte(`{"role":"user","content":[` + string(encoded) + `]}`)
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	if len(message.Content) != 1 {
		t.Fatalf("content = %d", len(message.Content))
	}
	return message.Content[0], nil
}

func TestToolResultJSON(t *testing.T) {
	result := llm.NewToolResultPart(llm.ToolResult{
		CallID:        "call-1",
		Name:          "lookup",
		Kind:          llm.ToolKindFunction,
		Status:        llm.ToolStatusCompleted,
		Content:       []llm.Part{llm.NewTextPart("ok")},
		Structured:    json.RawMessage(`{"n":1}`),
		InputRequests: json.RawMessage(`[]`),
	})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{
		`"type":"tool_result"`,
		`"call_id":"call-1"`,
		`"content":[{"type":"text","text":"ok"}]`,
		`"structured":{"n":1}`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("JSON %s does not contain %s", encoded, want)
		}
	}

	var decoded llm.ToolResultPart
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.CallID != "call-1" || decoded.Text() != "ok" || string(decoded.Structured) != `{"n":1}` {
		t.Errorf("decoded = %+v", decoded.ToolResult)
	}
}

func TestMessageJSON_AllPartTypes(t *testing.T) {
	message := llm.AssistantMessageWithParts(
		llm.NewReasoningPart("r"),
		llm.NewTextPart("t"),
		llm.FileFromBytes([]byte("x"), "image/png"),
		llm.NewToolCallPart(llm.ToolCall{CallID: "c", Name: "tool"}),
		llm.NewRefusalPart("no"),
	)
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded llm.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Content) != 5 {
		t.Fatalf("content = %d", len(decoded.Content))
	}
	want := []llm.PartType{
		llm.PartTypeReasoning,
		llm.PartTypeText,
		llm.PartTypeFile,
		llm.PartTypeToolCall,
		llm.PartTypeRefusal,
	}
	for i, part := range decoded.Content {
		if part.PartType() != want[i] {
			t.Errorf("content[%d] = %q, want %q", i, part.PartType(), want[i])
		}
	}
}

func TestMessageJSON_UnknownNestedTypeFailsClosed(t *testing.T) {
	raw := `{"role":"user","content":[{"type":"tool_result","call_id":"c","status":"completed","content":[{"type":"hologram"}]}]}`
	var message llm.Message
	err := json.Unmarshal([]byte(raw), &message)
	if !errors.Is(err, llm.ErrUnknownPartType) {
		t.Fatalf("Unmarshal = %v, want ErrUnknownPartType", err)
	}
}

func TestMessageJSON_DecodesToolCallKindDefault(t *testing.T) {
	var message llm.Message
	err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"tool_call","call_id":"c","name":"x"}]}`), &message)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	call := message.ToolCalls()[0]
	if call.Kind != llm.ToolKindFunction {
		t.Errorf("kind = %q", call.Kind)
	}
}
