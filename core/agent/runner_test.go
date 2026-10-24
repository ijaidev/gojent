package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// step is one scripted model response. Exactly one of response, err, or run is
// expected to be set.
type step struct {
	response *llm.Response
	err      error
	run      func(ctx context.Context, req *llm.Request) (*llm.Response, error)
	delay    time.Duration
}

type scriptModel struct {
	mu    sync.Mutex
	steps []step
	calls []llm.Request
	next  int
}

var _ llm.Model = (*scriptModel)(nil)

func (m *scriptModel) ModelID() string { return "scripted" }

func (m *scriptModel) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	m.calls = append(m.calls, *req.Clone())
	index := m.next
	if index >= len(m.steps) {
		m.mu.Unlock()
		return nil, fmt.Errorf("unexpected model call %d", index+1)
	}
	m.next++
	current := m.steps[index]
	m.mu.Unlock()

	if current.delay > 0 {
		timer := time.NewTimer(current.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if current.run != nil {
		return current.run(ctx, req)
	}
	return current.response, current.err
}

func (m *scriptModel) Calls() []llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]llm.Request, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *scriptModel) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func finalText(text string) step {
	return step{response: &llm.Response{
		Message:      llm.AssistantMessage(text),
		FinishReason: llm.FinishReasonStop,
	}}
}

func toolCallStep(calls ...llm.ToolCall) step {
	return step{response: &llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
		FinishReason: llm.FinishReasonTool,
	}}
}

type recordingStore struct {
	mu      sync.Mutex
	msgs    []llm.Message
	batches [][]llm.Message
}

var _ session.Store = (*recordingStore)(nil)

func (s *recordingStore) Load(_ context.Context, sess session.Session) ([]llm.Message, error) {
	if sess.ID == "" {
		return nil, session.ErrIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.Message, len(s.msgs))
	for i := range s.msgs {
		out[i] = s.msgs[i].Clone()
	}
	return out, nil
}

func (s *recordingStore) Append(_ context.Context, sess session.Session, messages ...llm.Message) error {
	if sess.ID == "" {
		return session.ErrIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := make([]llm.Message, len(messages))
	for i := range messages {
		batch[i] = messages[i].Clone()
	}
	s.msgs = append(s.msgs, batch...)
	s.batches = append(s.batches, batch)
	return nil
}

func (s *recordingStore) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.Message, len(s.msgs))
	for i := range s.msgs {
		out[i] = s.msgs[i].Clone()
	}
	return out
}

func (s *recordingStore) Batches() [][]llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]llm.Message, len(s.batches))
	for i := range s.batches {
		batch := s.batches[i]
		out[i] = make([]llm.Message, len(batch))
		for j := range batch {
			out[i][j] = batch[j].Clone()
		}
	}
	return out
}

type echoInput struct {
	Value string `json:"value" description:"Value to echo"`
}

// echoTool records every call it receives.
type echoRecorder struct {
	mu    sync.Mutex
	calls []tool.Call
}

func (r *echoRecorder) record(call tool.Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *echoRecorder) Calls() []tool.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]tool.Call, len(r.calls))
	copy(out, r.calls)
	return out
}

func newEchoTool(t *testing.T, name string, recorder *echoRecorder) tool.Tool {
	t.Helper()
	return tool.MustNew(tool.Config[echoInput, string]{
		ID:          name,
		Description: "Echo a value",
		Execute: func(_ context.Context, in echoInput) (string, error) {
			if recorder != nil {
				recorder.record(tool.Call{Arguments: []byte(`{"value":` + fmt.Sprintf("%q", in.Value) + `}`)})
			}
			return in.Value, nil
		},
	})
}

func mustAgent(t *testing.T, cfg agent.Config) *agent.Agent {
	t.Helper()
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}

func assertNoDanglingToolCalls(t *testing.T, messages []llm.Message) {
	t.Helper()
	pending := make(map[string]bool)
	for index := range messages {
		msg := &messages[index]
		switch msg.Role {
		case llm.RoleAssistant:
			if len(pending) > 0 {
				t.Fatalf("message %d starts a new assistant turn with unanswered tool calls: %v", index, pending)
			}
			for _, call := range msg.ToolCalls {
				if call.ID == "" {
					t.Fatalf("message %d has a tool call without an ID", index)
				}
				if pending[call.ID] {
					t.Fatalf("message %d repeats pending tool call ID %q", index, call.ID)
				}
				pending[call.ID] = true
			}
		case llm.RoleTool:
			if !pending[msg.ToolCallID] {
				t.Fatalf("message %d has a result for unknown call ID %q", index, msg.ToolCallID)
			}
			delete(pending, msg.ToolCallID)
		}
	}
	if len(pending) > 0 {
		t.Fatalf("transcript ends with unanswered tool calls: %v", pending)
	}
}

func TestAgent_Generate(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("hello")}}
	a := mustAgent(t, agent.Config{
		Name:         "assistant",
		Instructions: "be brief",
		Model:        model,
	})

	resp, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("Text = %q, want hello", resp.Text)
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("Messages = %d, want system, user, assistant", len(resp.Messages))
	}
	if resp.Messages[0].Role != llm.RoleSystem || resp.Messages[0].Text() != "be brief" {
		t.Errorf("unexpected system message: %+v", resp.Messages[0])
	}

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	if len(calls[0].Messages) != 2 {
		t.Fatalf("request messages = %d, want system and user", len(calls[0].Messages))
	}
}

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

func TestAgent_UniqueToolCallIDs(t *testing.T) {
	tests := []struct {
		name  string
		calls []llm.ToolCall
	}{
		{
			name: "empty ids",
			calls: []llm.ToolCall{
				{Name: "echo", Arguments: `{"value":"one"}`},
				{Name: "echo", Arguments: `{"value":"two"}`},
			},
		},
		{
			name: "duplicate ids",
			calls: []llm.ToolCall{
				{ID: "same", Name: "echo", Arguments: `{"value":"one"}`},
				{ID: "same", Name: "echo", Arguments: `{"value":"two"}`},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{steps: []step{
				toolCallStep(tc.calls...),
				finalText("done"),
			}}
			executed := 0
			echo := tool.MustNew(tool.Config[echoInput, string]{
				ID: "echo",
				Execute: func(_ context.Context, _ echoInput) (string, error) {
					executed++
					return "ok", nil
				},
			})
			a := mustAgent(t, agent.Config{Name: "multi", Model: model, Tools: []tool.Tool{echo}})
			store := &recordingStore{}

			resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if executed != 2 {
				t.Fatalf("executed tools = %d, want 2", executed)
			}
			if len(resp.ToolCalls) != 2 {
				t.Fatalf("response tool calls = %d, want 2", len(resp.ToolCalls))
			}
			if resp.ToolCalls[0].ID == resp.ToolCalls[1].ID {
				t.Fatalf("tool call IDs are not unique: %q", resp.ToolCalls[0].ID)
			}
			if resp.ToolCalls[0].ID == "" || resp.ToolCalls[1].ID == "" {
				t.Fatalf("tool call ID is empty: %+v", resp.ToolCalls)
			}
			for i, call := range resp.ToolCalls {
				if call.Index != i {
					t.Errorf("call %d index = %d, want %d", i, call.Index, i)
				}
			}
			assertNoDanglingToolCalls(t, store.Messages())
		})
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

func TestAgent_MaxStepsKeepsTranscriptComplete(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{Name: "echo", Arguments: `{"value":"one"}`}),
		toolCallStep(llm.ToolCall{Name: "echo", Arguments: `{"value":"two"}`}),
		finalText("never reached"),
	}}
	recorder := &echoRecorder{}
	echo := newEchoTool(t, "echo", recorder)
	a := mustAgent(t, agent.Config{Name: "looper", Model: model, Tools: []tool.Tool{echo}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Budget:  agent.Budget{MaxSteps: 2},
		Session: &session.Session{ID: "s"},
	})
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if resp == nil {
		t.Fatal("expected partial response")
	}
	if resp.FinishReason != llm.FinishReasonLength {
		t.Errorf("FinishReason = %q, want length", resp.FinishReason)
	}
	if model.CallCount() != 2 {
		t.Errorf("model calls = %d, want 2", model.CallCount())
	}
	if len(recorder.Calls()) != 2 {
		t.Errorf("executed tools = %d, want 2", len(recorder.Calls()))
	}
	messages := store.Messages()
	if len(messages) != 5 {
		t.Fatalf("stored messages = %d, want user + 2*(assistant+tool)", len(messages))
	}
	assertNoDanglingToolCalls(t, messages)
}

func TestAgent_WithMaxStepsOption(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "one", Name: "echo", Arguments: `{"value":"one"}`}),
	}}
	echo := newEchoTool(t, "echo", nil)
	a := mustAgent(t, agent.Config{Name: "looper", Model: model, Tools: []tool.Tool{echo}})

	_, err := a.Generate(context.Background(), "go", agent.WithMaxSteps(1))
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
}

func TestAgent_ToolEncodeFailureEmitsOneToolEnd(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call-1", Name: "bad", Arguments: `{"value":"x"}`}),
		finalText("done"),
	}}
	bad := tool.MustNew(tool.Config[echoInput, tool.Result]{
		ID: "bad",
		Execute: func(context.Context, echoInput) (tool.Result, error) {
			return tool.Result{
				Content: []llm.Part{llm.TextPart("ok")},
				Data:    make(chan int),
			}, nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "encode", Model: model, Tools: []tool.Tool{bad}})
	sink := &eventRecorder{}

	if _, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Sink: sink}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var toolEnds []agent.ToolEnd
	for _, event := range sink.Events() {
		if typed, ok := event.(agent.ToolEnd); ok {
			toolEnds = append(toolEnds, typed)
		}
	}
	if len(toolEnds) != 1 {
		t.Fatalf("ToolEnd events = %d, want 1", len(toolEnds))
	}
	if !errors.Is(toolEnds[0].Err, tool.ErrOutputEncode) {
		t.Errorf("ToolEnd err = %v, want ErrOutputEncode", toolEnds[0].Err)
	}
}

func TestAgent_ToolPanicIsContained(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "boom", Name: "boom", Arguments: `{}`}),
		finalText("recovered"),
	}}
	boom := tool.MustNew(tool.Config[struct{}, string]{
		ID: "boom",
		Execute: func(_ context.Context, _ struct{}) (string, error) {
			panic("tool exploded")
		},
	})
	a := mustAgent(t, agent.Config{Name: "panic", Model: model, Tools: []tool.Tool{boom}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("Text = %q", resp.Text)
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "panic: tool exploded") {
		t.Errorf("panic was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)

	// The next model request must include the error result, not a crash.
	calls := model.Calls()
	result := calls[1].Messages[2]
	if !result.IsError {
		t.Errorf("replayed tool result is not marked as an error: %+v", result)
	}
}

func TestAgent_ModelPanicIsContained(t *testing.T) {
	model := &scriptModel{steps: []step{{
		run: func(context.Context, *llm.Request) (*llm.Response, error) {
			panic("model exploded")
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "panic", Model: model})

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if resp != nil {
		t.Errorf("expected no response, got %+v", resp)
	}
	var panicErr *agent.PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("err = %T %v, want *agent.PanicError", err, err)
	}
	if !strings.Contains(panicErr.Error(), "model exploded") {
		t.Errorf("panic message = %q", panicErr.Error())
	}
}

func TestAgent_ToolTimeoutIsContained(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "slow", Name: "slow", Arguments: `{}`}),
		finalText("gave up"),
	}}
	slow := tool.MustNew(tool.Config[struct{}, string]{
		ID: "slow",
		Execute: func(_ context.Context, _ struct{}) (string, error) {
			<-release
			return "late", nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "timeout", Model: model, Tools: []tool.Tool{slow}})
	store := &recordingStore{}

	started := time.Now()
	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:       store,
		ToolTimeout: 25 * time.Millisecond,
		Session:     &session.Session{ID: "s"},
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("run took %v; a blocking tool was not contained", elapsed)
	}
	if resp.Text != "gave up" {
		t.Errorf("Text = %q", resp.Text)
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "deadline exceeded") {
		t.Errorf("timeout was not reported as an error result: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}

func TestAgent_CancellationPreservesCause(t *testing.T) {
	started := make(chan struct{})
	model := &scriptModel{steps: []step{{
		run: func(ctx context.Context, _ *llm.Request) (*llm.Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}}
	a := mustAgent(t, agent.Config{Name: "cancel", Model: model})
	run, err := a.StreamWith(context.Background(), "go", agent.RunOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model was never called")
	}
	stop := errors.New("stop requested by test")
	run.Cancel(stop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := run.Wait(ctx)
	if !errors.Is(err, stop) {
		t.Fatalf("Wait err = %v, want cancellation cause", err)
	}
	if resp != nil {
		t.Errorf("expected no response, got %+v", resp)
	}

	for range run.Events() {
		// Drain so the dispatcher can close the channel.
	}
}

func TestAgent_PolicyDeniesAndApproves(t *testing.T) {
	tests := []struct {
		name          string
		decision      agent.Decision
		approver      agent.Approver
		wantExecuted  bool
		terminalError error
	}{
		{name: "allow", decision: agent.Allow, wantExecuted: true},
		{name: "deny", decision: agent.Deny, wantExecuted: false},
		{name: "ask without approver", decision: agent.Ask, wantExecuted: false},
		{
			name:         "ask approved",
			decision:     agent.Ask,
			approver:     agent.ApproverFunc(func(context.Context, agent.ToolRequest) (bool, error) { return true, nil }),
			wantExecuted: true,
		},
		{
			name:         "ask rejected",
			decision:     agent.Ask,
			approver:     agent.ApproverFunc(func(context.Context, agent.ToolRequest) (bool, error) { return false, nil }),
			wantExecuted: false,
		},
		{name: "terminate", decision: agent.Terminate, wantExecuted: false, terminalError: agent.ErrTerminated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executed := false
			risky := tool.MustNew(tool.Config[struct{}, string]{
				ID: "risky",
				Execute: func(_ context.Context, _ struct{}) (string, error) {
					executed = true
					return "acted", nil
				},
			})
			model := &scriptModel{steps: []step{
				toolCallStep(llm.ToolCall{ID: "r1", Name: "risky", Arguments: `{}`}),
				finalText("done"),
			}}
			a := mustAgent(t, agent.Config{Name: "policy", Model: model, Tools: []tool.Tool{risky}})
			store := &recordingStore{}
			policy := agent.Hooks{
				OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
					return tc.decision, nil
				},
			}

			resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
				Store:    store,
				Policy:   policy,
				Approver: tc.approver,
				Session:  &session.Session{ID: "s"},
			})

			if tc.terminalError != nil {
				if !errors.Is(err, tc.terminalError) {
					t.Fatalf("err = %v, want %v", err, tc.terminalError)
				}
				if resp != nil {
					t.Errorf("expected no response on termination")
				}
				// The assistant tool call must not be persisted without results.
				if len(store.Messages()) != 1 {
					t.Errorf("terminated run stored %d messages, want only the user input", len(store.Messages()))
				}
				for _, msg := range store.Messages() {
					if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
						t.Fatalf("terminated run persisted an unanswered tool call")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if executed != tc.wantExecuted {
				t.Errorf("executed = %v, want %v", executed, tc.wantExecuted)
			}
			if tc.wantExecuted {
				if resp.Text != "done" {
					t.Errorf("Text = %q", resp.Text)
				}
				assertNoDanglingToolCalls(t, store.Messages())
			} else {
				messages := store.Messages()
				if len(messages) != 4 {
					t.Fatalf("stored messages = %d", len(messages))
				}
				if !messages[2].IsError {
					t.Errorf("denied call was not reported as an error: %+v", messages[2])
				}
				assertNoDanglingToolCalls(t, messages)
			}
		})
	}
}

func TestAgent_NilResponseIsAnError(t *testing.T) {
	model := &scriptModel{steps: []step{{}}}
	a := mustAgent(t, agent.Config{Name: "empty", Model: model})

	if _, err := a.Generate(context.Background(), "go"); !errors.Is(err, agent.ErrResponseRequired) {
		t.Fatalf("err = %v, want ErrResponseRequired", err)
	}
}

func TestAgent_PolicyCanReplaceModelRequest(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("done")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	policy := agent.Hooks{
		OnBeforeModel: func(_ context.Context, req *agent.ModelRequest) error {
			req.Request = &llm.Request{Messages: []llm.Message{llm.UserMessage("replacement")}}
			return nil
		},
	}

	if _, err := a.Generate(context.Background(), "original", agent.WithPolicy(policy)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	messages := model.Calls()[0].Messages
	if len(messages) != 1 || messages[0].Text() != "replacement" {
		t.Fatalf("model request messages = %+v, want the replacement", messages)
	}
}

func TestAgent_PolicyCanReplaceModelResponse(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("original")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	policy := agent.Hooks{
		OnAfterModel: func(_ context.Context, result *agent.ModelResult) {
			result.Response = &llm.Response{
				Message:      llm.AssistantMessage("replaced"),
				FinishReason: llm.FinishReasonStop,
			}
		},
	}

	resp, err := a.Generate(context.Background(), "go", agent.WithPolicy(policy))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Text != "replaced" {
		t.Errorf("Text = %q, want replaced", resp.Text)
	}
}

func TestAgent_PolicyMutationDoesNotAlterTranscript(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("done")}}
	a := mustAgent(t, agent.Config{Name: "policy", Model: model})
	store := &recordingStore{}
	policy := agent.Hooks{
		OnBeforeModel: func(_ context.Context, req *agent.ModelRequest) error {
			for i := range req.Request.Messages {
				if req.Request.Messages[i].Role != llm.RoleUser {
					continue
				}
				req.Request.Messages[i].Content[0].Text = "redacted"
			}
			return nil
		},
	}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Policy:  policy,
		Session: &session.Session{ID: "s"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, message := range resp.Messages {
		if message.Role == llm.RoleUser && message.Text() != "go" {
			t.Errorf("response user message = %q, want the original content", message.Text())
		}
	}
	for _, message := range store.Messages() {
		if message.Role == llm.RoleUser && message.Text() != "go" {
			t.Errorf("stored user message = %q, want the original content", message.Text())
		}
	}
}

func TestAgent_ToolCallIDCollisionWithGeneratedID(t *testing.T) {
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "call_1_0", Name: "echo", Arguments: `{"value":"one"}`}),
		toolCallStep(llm.ToolCall{ID: "call_1_0", Name: "echo", Arguments: `{"value":"two"}`}),
		finalText("done"),
	}}
	echo := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(context.Context, echoInput) (string, error) {
			return "ok", nil
		},
	})
	a := mustAgent(t, agent.Config{Name: "collision", Model: model, Tools: []tool.Tool{echo}})

	resp, err := a.Generate(context.Background(), "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := make(map[string]bool, len(resp.ToolCalls))
	for _, call := range resp.ToolCalls {
		if seen[call.ID] {
			t.Fatalf("duplicate tool call ID %q in %+v", call.ID, resp.ToolCalls)
		}
		seen[call.ID] = true
	}
}

func TestAgent_ToolBudgetPersistsAllResults(t *testing.T) {
	executed := 0
	echo := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(_ context.Context, _ echoInput) (string, error) {
			executed++
			return "ok", nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(
			llm.ToolCall{ID: "a", Name: "echo", Arguments: `{"value":"a"}`},
			llm.ToolCall{ID: "b", Name: "echo", Arguments: `{"value":"b"}`},
		),
		finalText("never reached"),
	}}
	a := mustAgent(t, agent.Config{Name: "budget", Model: model, Tools: []tool.Tool{echo}})
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Budget:  agent.Budget{MaxToolCalls: 1},
		Session: &session.Session{ID: "s"},
	})
	if !errors.Is(err, agent.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if resp == nil {
		t.Fatal("expected a partial response")
	}
	if executed != 1 {
		t.Errorf("executed tools = %d, want 1", executed)
	}
	if model.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", model.CallCount())
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d, want user, assistant, and two results", len(messages))
	}
	assertNoDanglingToolCalls(t, messages)
	if messages[2].IsError {
		t.Errorf("first result should have executed: %+v", messages[2])
	}
	if !messages[3].IsError {
		t.Errorf("over-budget result is not marked as an error: %+v", messages[3])
	}
}

func TestAgent_TokenBudget(t *testing.T) {
	t.Run("final answer", func(t *testing.T) {
		response := finalText("done")
		response.response.Usage = llm.Usage{PromptTokens: 40, CompletionTokens: 60, TotalTokens: 100}
		model := &scriptModel{steps: []step{response}}
		a := mustAgent(t, agent.Config{Name: "tokens", Model: model})
		store := &recordingStore{}

		resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
			Store:   store,
			Budget:  agent.Budget{MaxTotalTokens: 50},
			Session: &session.Session{ID: "s"},
		})
		if !errors.Is(err, agent.ErrBudgetExceeded) {
			t.Fatalf("err = %v, want ErrBudgetExceeded", err)
		}
		if resp == nil || resp.Text != "done" {
			t.Fatalf("expected the final answer with the budget error, got %+v", resp)
		}
		if len(store.Messages()) != 2 {
			t.Errorf("stored messages = %d, want user and assistant", len(store.Messages()))
		}
	})

	t.Run("tool step stops before next model call", func(t *testing.T) {
		first := &llm.Response{
			Message:      llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"value":"x"}`}}},
			FinishReason: llm.FinishReasonTool,
			Usage:        llm.Usage{PromptTokens: 40, CompletionTokens: 60, TotalTokens: 100},
		}
		model := &scriptModel{steps: []step{{response: first}, finalText("never reached")}}
		echo := newEchoTool(t, "echo", nil)
		a := mustAgent(t, agent.Config{Name: "tokens", Model: model, Tools: []tool.Tool{echo}})
		store := &recordingStore{}

		_, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
			Store:   store,
			Budget:  agent.Budget{MaxTotalTokens: 50},
			Session: &session.Session{ID: "s"},
		})
		if !errors.Is(err, agent.ErrBudgetExceeded) {
			t.Fatalf("err = %v, want ErrBudgetExceeded", err)
		}
		if model.CallCount() != 1 {
			t.Errorf("model calls = %d, want 1", model.CallCount())
		}
		messages := store.Messages()
		if len(messages) != 3 {
			t.Fatalf("stored messages = %d, want user, assistant, result", len(messages))
		}
		assertNoDanglingToolCalls(t, messages)
	})
}

func TestAgent_RetriesTransientModelFailures(t *testing.T) {
	model := &scriptModel{steps: []step{
		{err: &llm.Error{Kind: llm.KindRateLimit, RetryAfter: time.Millisecond}},
		finalText("recovered"),
	}}
	a := mustAgent(t, agent.Config{Name: "retry", Model: model})
	opts := agent.RunOptions{Retry: agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond}}

	resp, err := a.GenerateWith(context.Background(), "go", opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "recovered" || model.CallCount() != 2 {
		t.Errorf("Text = %q, calls = %d", resp.Text, model.CallCount())
	}
}

func TestAgent_DoesNotRetryInvalidRequests(t *testing.T) {
	model := &scriptModel{steps: []step{{err: &llm.Error{Kind: llm.KindInvalid}}}}
	a := mustAgent(t, agent.Config{Name: "invalid", Model: model})
	opts := agent.RunOptions{Retry: agent.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond}}

	_, err := a.GenerateWith(context.Background(), "go", opts)
	if err == nil || model.CallCount() != 1 {
		t.Fatalf("err = %v, calls = %d; want one non-retried call", err, model.CallCount())
	}
}

func TestAgent_ValidatesToolArguments(t *testing.T) {
	executed := false
	strict := tool.MustNew(tool.Config[echoInput, string]{
		ID: "echo",
		Execute: func(_ context.Context, _ echoInput) (string, error) {
			executed = true
			return "ok", nil
		},
	})
	model := &scriptModel{steps: []step{
		toolCallStep(llm.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}),
		finalText("done"),
	}}
	a := mustAgent(t, agent.Config{Name: "validation", Model: model, Tools: []tool.Tool{strict}})
	store := &recordingStore{}

	if _, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{Store: store, Session: &session.Session{ID: "s"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed {
		t.Fatal("tool handler ran with invalid arguments")
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d", len(messages))
	}
	if !messages[2].IsError || !strings.Contains(messages[2].Text(), "missing required property") {
		t.Errorf("validation failure was not reported to the model: %+v", messages[2])
	}
	assertNoDanglingToolCalls(t, messages)
}
