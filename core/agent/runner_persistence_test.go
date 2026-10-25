package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

func TestAgent_ToolLoopPersistsAtomicBatches(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "c1", Name: "echo", Arguments: `{"value":"sunny"}`}),
		finalText("It is sunny"),
	}}
	recorder := &echoRecorder{}
	echo := newEchoTool(t, "echo", recorder)
	a := mustAgent(t, agent.Config{Name: "weather", Model: model, Tools: []tool.Tool{echo}})
	store := &recordingStore{}
	sess := &session.Session{ID: "thread-1", ResourceID: "user-1"}

	resp, err := a.GenerateWith(context.Background(), "weather?", agent.RunOptions{Store: store, Session: sess})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "It is sunny" {
		t.Errorf("Text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "c1" {
		t.Fatalf("unexpected response tool calls: %+v", resp.ToolCalls)
	}
	if got := recorder.Calls(); len(got) != 1 {
		t.Fatalf("executed tools = %d, want 1", len(got))
	}

	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d, want user, assistant, tool, assistant", len(messages))
	}
	if messages[0].Role != llm.RoleUser || messages[1].Role != llm.RoleAssistant || messages[2].Role != llm.RoleTool || messages[3].Role != llm.RoleAssistant {
		t.Fatalf("unexpected roles: %+v", messages)
	}
	if messages[1].ToolCalls[0].ID != messages[2].ToolCallID {
		t.Errorf("tool call ID %q does not match result ID %q", messages[1].ToolCalls[0].ID, messages[2].ToolCallID)
	}
	assertNoDanglingToolCalls(t, messages)

	batches := store.Batches()
	if len(batches) != 3 {
		t.Fatalf("append batches = %d, want input, assistant+tools, final assistant", len(batches))
	}
	if len(batches[1]) != 2 {
		t.Errorf("assistant+tool batch size = %d, want 2", len(batches[1]))
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	second := calls[1].Messages
	if len(second) != 3 {
		t.Fatalf("second request messages = %d, want user, assistant, tool", len(second))
	}
	if second[1].Role != llm.RoleAssistant || second[1].ToolCalls[0].ID != "c1" {
		t.Errorf("assistant replay missing tool call: %+v", second[1])
	}
	if second[2].Role != llm.RoleTool || second[2].ToolCallID != "c1" || second[2].Text() != "sunny" {
		t.Errorf("unexpected tool result replay: %+v", second[2])
	}
}

func TestAgent_ReplayAfterJSONPersistence(t *testing.T) {
	providerData := &llm.ProviderData{Provider: "test", Version: 1, Data: []byte(`{"signature":"abc"}`)}
	firstResponse := &llm.Response{
		Message: llm.Message{
			Role:         llm.RoleAssistant,
			Content:      []llm.Part{llm.TextPart("")},
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"value":"persisted"}`}},
			ProviderData: providerData,
		},
		FinishReason: llm.FinishReasonTool,
	}
	firstModel := &scriptModel{steps: []step{
		{response: firstResponse},
		finalText("first done"),
	}}
	echo := newEchoTool(t, "echo", nil)
	a := mustAgent(t, agent.Config{Name: "replay", Model: firstModel, Tools: []tool.Tool{echo}})
	store := &recordingStore{}
	sess := &session.Session{ID: "persisted-thread"}

	if _, err := a.GenerateWith(context.Background(), "start", agent.RunOptions{Store: store, Session: sess}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Simulate a store that serializes to JSON and reloads later.
	encoded, err := json.Marshal(store.Messages())
	if err != nil {
		t.Fatalf("marshal stored messages: %v", err)
	}
	var reloaded []llm.Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatalf("unmarshal stored messages: %v", err)
	}
	if len(reloaded[1].Content) != 0 {
		t.Errorf("empty text part survived JSON persistence: %+v", reloaded[1].Content)
	}
	if reloaded[1].ProviderData == nil || string(reloaded[1].ProviderData.Data) != `{"signature":"abc"}` {
		t.Fatalf("provider data did not survive JSON persistence: %+v", reloaded[1].ProviderData)
	}

	replayStore := &recordingStore{}
	replayStore.msgs = reloaded
	replayModel := &scriptModel{steps: []step{finalText("second done")}}
	replayAgent := mustAgent(t, agent.Config{Name: "replay", Model: replayModel, Tools: []tool.Tool{echo}})

	resp, err := replayAgent.GenerateWith(context.Background(), "again", agent.RunOptions{Store: replayStore, Session: sess})
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	if resp.Text != "second done" {
		t.Fatalf("replay Text = %q", resp.Text)
	}

	request := replayModel.Calls()[0]
	var assistant *llm.Message
	var result *llm.Message
	for i := range request.Messages {
		msg := &request.Messages[i]
		switch {
		case msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0:
			assistant = msg
		case msg.Role == llm.RoleTool && msg.ToolCallID == "c1":
			result = msg
		}
	}
	if assistant == nil || result == nil {
		t.Fatalf("replayed request lost assistant/tool pair: %+v", request.Messages)
	}
	if assistant.ProviderData == nil || assistant.ProviderData.Provider != "test" {
		t.Errorf("replayed assistant lost provider data: %+v", assistant.ProviderData)
	}
	if result.Text() != "persisted" {
		t.Errorf("replayed tool result = %q", result.Text())
	}
	assertNoDanglingToolCalls(t, append(reloaded, finalAssistant("second done")))
}

func finalAssistant(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.Part{llm.TextPart(text)}}
}
