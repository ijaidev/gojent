package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

type streamModel struct {
	mu    sync.Mutex
	steps [][]llm.StreamEvent
	calls []llm.Request
	next  int
}

var (
	_ llm.Model    = (*streamModel)(nil)
	_ llm.Streamer = (*streamModel)(nil)
)

func (m *streamModel) ModelID() string { return "stream-scripted" }

func (m *streamModel) Info() llm.Info {
	return llm.Info{
		Provider: "test",
		Model:    "stream-scripted",
		Features: llm.Features{
			Streaming: true,
			Tools:     true,
			Reasoning: true,
			Vision:    true,
			AudioIn:   true,
			Video:     true,
			Documents: true,
		},
	}
}

func (m *streamModel) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, fmt.Errorf("Generate should not be called when Stream is available")
}

func (m *streamModel) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	m.calls = append(m.calls, *req.Clone())
	index := m.next
	if index >= len(m.steps) {
		m.mu.Unlock()
		return nil, fmt.Errorf("unexpected stream call %d", index+1)
	}
	m.next++
	events := m.steps[index]
	m.mu.Unlock()
	return llm.NewSliceStream(ctx, events), nil
}

func (m *streamModel) Calls() []llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]llm.Request, len(m.calls))
	copy(out, m.calls)
	return out
}

type eventRecorder struct {
	mu     sync.Mutex
	events []agent.Event
}

func (r *eventRecorder) Emit(event agent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRecorder) Events() []agent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]agent.Event, len(r.events))
	copy(out, r.events)
	return out
}

func TestAgent_StreamingMergesFragmentsAndEmitsEvents(t *testing.T) {
	stepUsage := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	finalUsage := llm.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}
	provider := llm.ProviderData{"test": json.RawMessage(`{"thought":"sig"}`)}

	model := &streamModel{steps: [][]llm.StreamEvent{
		{
			llm.MessageStart{ID: "resp-1", Model: "stream-scripted"},
			llm.PartStart{Index: 0, Part: llm.NewTextPart("")},
			llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "Hel"},
			llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "lo "},
			llm.PartStart{Index: 1, Part: llm.NewReasoningPart("")},
			llm.PartDelta{Index: 1, Kind: llm.PartTypeReasoning, Text: "think", Provider: provider},
			llm.PartStart{Index: 2, Part: llm.NewToolCallPart(llm.ToolCall{})},
			llm.PartDelta{Index: 2, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
				Name: "echo", InputFragment: `{"value":`,
			}},
			llm.PartDelta{Index: 2, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
				CallID: "c1", InputFragment: `"stream"}`,
			}},
			llm.PartEnd{Index: 0},
			llm.PartEnd{Index: 1},
			llm.PartEnd{Index: 2},
			llm.MessageEnd{
				Status:       llm.MessageStatusCompleted,
				FinishReason: llm.FinishReasonTool,
				Usage:        stepUsage,
			},
		},
		{
			llm.MessageStart{ID: "resp-2", Model: "stream-scripted"},
			llm.PartStart{Index: 0, Part: llm.NewTextPart("")},
			llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "done"},
			llm.PartEnd{Index: 0},
			llm.MessageEnd{
				Status:       llm.MessageStatusCompleted,
				FinishReason: llm.FinishReasonStop,
				Usage:        finalUsage,
			},
		},
	}}

	recorder := &echoRecorder{}
	echo := newEchoTool(t, "echo", recorder)
	a := mustAgent(t, agent.Config{Name: "streamer", Model: model, Tools: []tool.Tool{echo}})
	sink := &eventRecorder{}
	store := &recordingStore{}

	resp, err := a.GenerateWith(context.Background(), "go", agent.RunOptions{
		Store:   store,
		Sink:    sink,
		Session: &session.Session{ID: "s"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "done" {
		t.Errorf("Text = %q, want done", resp.Text)
	}
	if resp.Usage != (llm.Usage{PromptTokens: 13, CompletionTokens: 7, TotalTokens: 20}) {
		t.Errorf("Usage = %+v, want cumulative usage", resp.Usage)
	}
	if got := recorder.Calls(); len(got) != 1 {
		t.Fatalf("executed tools = %d, want 1", len(got))
	}

	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d, want user, assistant, tool, assistant", len(messages))
	}
	firstAssistant := messages[1]
	if firstAssistant.Text() != "Hello " || reasoningTextOf(firstAssistant) != "think" {
		t.Errorf("streamed assistant content = %q / %q", firstAssistant.Text(), reasoningTextOf(firstAssistant))
	}
	calls := firstAssistant.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	if calls[0].CallID != "c1" || string(calls[0].Input) != `{"value":"stream"}` {
		t.Errorf("merged tool call = %+v", calls[0])
	}
	reasoning, ok := firstAssistant.Content[1].(*llm.ReasoningPart)
	if !ok {
		t.Fatalf("content[1] = %T, want reasoning", firstAssistant.Content[1])
	}
	if string(reasoning.Provider["test"]) != `{"thought":"sig"}` {
		t.Errorf("provider data not attached to streamed reasoning part: %+v", reasoning.Provider)
	}

	replay := model.Calls()[1].Messages
	var replayedAssistant *llm.Message
	for i := range replay {
		if replay[i].Role == llm.RoleAssistant && len(replay[i].ToolCalls()) > 0 {
			replayedAssistant = &replay[i]
		}
	}
	if replayedAssistant == nil {
		t.Fatalf("streamed assistant was not replayed: %+v", replay)
	}
	replayedReasoning, ok := replayedAssistant.Content[1].(*llm.ReasoningPart)
	if !ok || string(replayedReasoning.Provider["test"]) != `{"thought":"sig"}` {
		t.Fatalf("provider data was not replayed to the next model call: %+v", replayedAssistant)
	}
	assertNoDanglingToolCalls(t, messages)

	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("no events were emitted")
	}
	var text, thinking string
	var deltas int
	var messageStarts, messageEnds, toolStarts, toolEnds int
	for _, event := range events {
		switch typed := event.(type) {
		case agent.TextDelta:
			text += typed.Text
			deltas++
		case agent.ThinkingDelta:
			thinking += typed.Text
		case agent.MessageStart:
			messageStarts++
		case agent.MessageEnd:
			messageEnds++
			if typed.Message == nil {
				t.Error("MessageEnd has a nil message")
			}
		case agent.ToolStart:
			toolStarts++
		case agent.ToolEnd:
			toolEnds++
		}
	}
	if text != "Hello done" {
		t.Errorf("streamed text deltas = %q, want Hello done", text)
	}
	if thinking != "think" {
		t.Errorf("thinking deltas = %q, want think", thinking)
	}
	if messageStarts != 2 || messageEnds != 2 {
		t.Errorf("message events = %d starts / %d ends, want 2/2", messageStarts, messageEnds)
	}
	if toolStarts != 1 || toolEnds != 1 {
		t.Errorf("tool events = %d starts / %d ends, want 1/1", toolStarts, toolEnds)
	}
	last, ok := events[len(events)-1].(agent.RunEnd)
	if !ok {
		t.Fatalf("last event = %T, want RunEnd", events[len(events)-1])
	}
	if last.Response == nil || last.Response.Text != "done" || last.Err != nil {
		t.Errorf("unexpected RunEnd: %+v", last)
	}
}

func TestAgent_NonStreamingEmitsEquivalentEvents(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("hello")}}
	a := mustAgent(t, agent.Config{Name: "events", Model: model})
	sink := &eventRecorder{}

	if _, err := a.GenerateWith(context.Background(), "hi", agent.RunOptions{Sink: sink}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var text string
	var starts, ends int
	for _, event := range sink.Events() {
		switch typed := event.(type) {
		case agent.MessageStart:
			starts++
		case agent.TextDelta:
			text += typed.Text
		case agent.MessageEnd:
			ends++
		}
	}
	if text != "hello" {
		t.Errorf("text deltas = %q", text)
	}
	if starts != 1 || ends != 1 {
		t.Errorf("message events = %d starts / %d ends, want 1/1", starts, ends)
	}
}

func TestAgent_PanickingSinkDoesNotStarveRunEvents(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("hi")}}
	a := mustAgent(t, agent.Config{Name: "sink", Model: model})
	sink := agent.SinkFunc(func(agent.Event) {
		panic("observer failed")
	})

	run, err := a.Stream(context.Background(), "hello", agent.WithSink(sink))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var runEnd agent.RunEnd
	for event := range run.Events() {
		if typed, ok := event.(agent.RunEnd); ok {
			runEnd = typed
		}
	}
	if runEnd.Response == nil || runEnd.Response.Text != "hi" {
		t.Fatalf("RunEnd = %+v, want the completed response", runEnd)
	}
}

func TestAgent_StreamDrainsEventsAndReturnsResponse(t *testing.T) {
	model := &streamModel{steps: [][]llm.StreamEvent{{
		llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "hi"},
		llm.MessageEnd{Status: llm.MessageStatusCompleted, FinishReason: llm.FinishReasonStop},
	}}}
	a := mustAgent(t, agent.Config{Name: "async", Model: model})

	run, err := a.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var events int
	var runEnd agent.RunEnd
	for event := range run.Events() {
		events++
		if typed, ok := event.(agent.RunEnd); ok {
			runEnd = typed
		}
	}
	if events == 0 {
		t.Fatal("expected events")
	}
	if runEnd.Response == nil || runEnd.Response.Text != "hi" {
		t.Errorf("unexpected RunEnd: %+v", runEnd)
	}

	resp, err := run.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if resp.Text != "hi" {
		t.Errorf("Text = %q", resp.Text)
	}
}

// reasoningTextOf joins readable reasoning and summaries in part order.
func reasoningTextOf(message llm.Message) string {
	var out string
	for _, part := range message.Content {
		reasoning, ok := part.(*llm.ReasoningPart)
		if !ok {
			continue
		}
		out += reasoning.Text
		for _, summary := range reasoning.Summary {
			out += summary
		}
	}
	return out
}
