package agent_test

import (
	"context"
	"fmt"
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
