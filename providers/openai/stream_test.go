package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
	"github.com/openai/openai-go/responses"
)

// decodeStreamEvent decodes a fixture through the pinned SDK.
func decodeStreamEvent(t *testing.T, value map[string]any) responses.ResponseStreamEventUnion {
	t.Helper()
	encoded := mustJSON(t, value)
	var event responses.ResponseStreamEventUnion
	if err := json.Unmarshal(encoded, &event); err != nil {
		t.Fatalf("decode stream event: %v", err)
	}
	return event
}

func newTestStreamState(t *testing.T) (state *streamState, events chan llm.StreamEvent) {
	t.Helper()
	events = make(chan llm.StreamEvent, 64)
	return newStreamState(context.Background(), events, "gpt-test"), events
}

func applyStreamEvents(t *testing.T, state *streamState, values ...map[string]any) {
	t.Helper()
	for _, value := range values {
		if err := state.apply(decodeStreamEvent(t, value)); err != nil {
			t.Fatalf("apply %q: %v", value["type"], err)
		}
	}
}

func drainStreamEvents(events chan llm.StreamEvent) []llm.StreamEvent {
	var drained []llm.StreamEvent
	for {
		select {
		case event := <-events:
			drained = append(drained, event)
		default:
			return drained
		}
	}
}

func TestStreamEmitsCumulativeArgumentSnapshots(t *testing.T) {
	state, events := newTestStreamState(t)
	added := functionCallItem("fc_1", "call_1", "weather", "", "in_progress")
	final := functionCallItem("fc_1", "call_1", "weather", `{"city":"Tokyo"}`, "completed")
	response := responseFixture([]any{final}, "completed")

	applyStreamEvents(t, state,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		outputItemAddedEvent(1, 0, added),
		functionArgsDeltaEvent(2, 0, "fc_1", `{"city":`),
		functionArgsDeltaEvent(3, 0, "fc_1", `"Tokyo"}`),
		functionArgsDoneEvent(4, 0, "fc_1", `{"city":"Tokyo"}`),
		outputItemDoneEvent(5, 0, final),
		responseCompletedEvent(response),
	)
	if err := state.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var fragments []string
	for _, event := range drainStreamEvents(events) {
		delta, ok := event.(llm.PartDelta)
		if !ok || delta.Call == nil || !delta.Replace {
			continue
		}
		if delta.Call.InputFragment != "" {
			fragments = append(fragments, delta.Call.InputFragment)
		}
	}
	if len(fragments) < 2 {
		t.Fatalf("fragments = %v, want at least two cumulative snapshots", fragments)
	}
	if fragments[0] != `{"city":` || fragments[1] != `{"city":"Tokyo"}` {
		t.Fatalf("fragments = %v", fragments)
	}
}

func TestStreamBuffersReasoningUntilItemDone(t *testing.T) {
	state, events := newTestStreamState(t)
	final := reasoningItem("rs_1", "thinking", "encrypted-1", "completed")
	response := responseFixture([]any{final}, "completed")

	applyStreamEvents(t, state,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		outputItemAddedEvent(1, 0, reasoningItem("rs_1", "", "", "in_progress")),
		map[string]any{
			"type":            "response.reasoning_summary_text.delta",
			"sequence_number": 2,
			"item_id":         "rs_1",
			"output_index":    0,
			"summary_index":   0,
			"delta":           "thinking",
		},
	)
	for _, event := range drainStreamEvents(events) {
		if _, ok := event.(llm.PartStart); ok {
			t.Fatalf("reasoning started before the item finished: %T", event)
		}
	}

	applyStreamEvents(t, state,
		outputItemDoneEvent(3, 0, final),
		responseCompletedEvent(response),
	)
	if err := state.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var started, ended bool
	for _, event := range drainStreamEvents(events) {
		switch value := event.(type) {
		case llm.PartStart:
			if value.Index != 0 {
				t.Fatalf("reasoning index = %d", value.Index)
			}
			part, ok := value.Part.(*llm.ReasoningPart)
			if !ok || len(part.Summary) != 1 || part.Summary[0] != "thinking" {
				t.Fatalf("part = %+v", value.Part)
			}
			started = true
		case llm.PartEnd:
			ended = true
		}
	}
	if !started || !ended {
		t.Fatalf("started = %v ended = %v", started, ended)
	}
}

func TestStreamBuffersHostedItemUntilDone(t *testing.T) {
	state, events := newTestStreamState(t)
	final := webSearchItem("ws_1", "go", "completed")
	response := responseFixture([]any{final}, "completed")

	applyStreamEvents(t, state,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		outputItemAddedEvent(1, 0, webSearchItem("ws_1", "", "in_progress")),
	)
	for _, event := range drainStreamEvents(events) {
		if _, ok := event.(llm.PartStart); ok {
			t.Fatalf("hosted item started before it finished: %T", event)
		}
	}

	applyStreamEvents(t, state,
		outputItemDoneEvent(2, 0, final),
		responseCompletedEvent(response),
	)
	if err := state.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	var call *llm.ToolCallPart
	for _, event := range drainStreamEvents(events) {
		if start, ok := event.(llm.PartStart); ok {
			part, ok := start.Part.(*llm.ToolCallPart)
			if ok {
				call = part
			}
		}
	}
	if call == nil {
		t.Fatal("hosted call was not emitted")
	}
	if call.EffectiveKind() != llm.ToolKindServer || call.Name != "web_search" {
		t.Fatalf("call = %+v", call.ToolCall)
	}
	stateOfCall, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if len(stateOfCall.ServerItem) == 0 {
		t.Fatal("hosted item was not recorded")
	}
}

func TestStreamIncompleteResponse(t *testing.T) {
	message := messageItem("msg_1", "partial")
	incomplete := responseFixture([]any{message}, "incomplete")
	incomplete["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}

	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
			contentPartAddedEvent(2, 0, 0, "msg_1", ""),
			textDeltaEvent(3, 0, 0, "msg_1", "partial"),
			textDoneEvent(4, 0, 0, "msg_1", "partial"),
			contentPartDoneEvent(5, 0, 0, "msg_1", "partial"),
			outputItemDoneEvent(6, 0, message),
			map[string]any{
				"type":            "response.incomplete",
				"sequence_number": 7,
				"response":        incomplete,
			},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	response := llmtest.Collect(t, stream)
	if response.Status != llm.MessageStatusIncomplete {
		t.Fatalf("status = %q", response.Status)
	}
	if response.FinishReason != llm.FinishReasonLength || response.RawFinishReason != "max_output_tokens" {
		t.Fatalf("finish = %q raw = %q", response.FinishReason, response.RawFinishReason)
	}
	if len(response.Message.Content) != 1 {
		t.Fatalf("content = %+v", response.Message.Content)
	}
	text, ok := response.Message.Content[0].(*llm.TextPart)
	if !ok || text.Text != "partial" {
		t.Fatalf("part = %+v", response.Message.Content[0])
	}
}

func TestStreamFailedResponse(t *testing.T) {
	failed := responseFixture([]any{}, "failed")
	failed["error"] = map[string]any{"code": "rate_limit_exceeded", "message": "busy"}

	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			map[string]any{
				"type":            "response.failed",
				"sequence_number": 1,
				"response":        failed,
			},
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("err = %v, want *llm.Error", err)
	}
	if llmErr.Kind != llm.KindRateLimit {
		t.Fatalf("kind = %q", llmErr.Kind)
	}
}

func TestStreamErrorEvent(t *testing.T) {
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, map[string]any{
			"type":            "error",
			"sequence_number": 0,
			"code":            "rate_limit_exceeded",
			"message":         "busy",
			"param":           "",
		})
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("err = %v, want *llm.Error", err)
	}
	if llmErr.Kind != llm.KindRateLimit {
		t.Fatalf("kind = %q", llmErr.Kind)
	}
}

func TestStreamRejectsUnknownEvent(t *testing.T) {
	err := streamErrorForEvents(t, map[string]any{
		"type":            "response.future.event",
		"sequence_number": 0,
	})
	if !errors.Is(err, llm.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestStreamRejectsEventAfterTerminal(t *testing.T) {
	response := responseFixture([]any{messageItem("msg_1", "hello")}, "completed")
	err := streamErrorForEvents(t,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		responseCompletedEvent(response),
		textDeltaEvent(101, 0, 0, "msg_1", "late"),
	)
	if !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("err = %v, want ErrInvalidMessage", err)
	}
}

func TestStreamRequiresTerminalResponse(t *testing.T) {
	err := streamErrorForEvents(t,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
		contentPartAddedEvent(2, 0, 0, "msg_1", ""),
		textDeltaEvent(3, 0, 0, "msg_1", "hello"),
	)
	if !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("err = %v, want ErrInvalidMessage", err)
	}
}

func TestStreamRejectsEmptyResponse(t *testing.T) {
	empty := responseFixture([]any{}, "completed")
	err := streamErrorForEvents(t,
		responseCreatedEvent(responseFixture(nil, "in_progress")),
		responseCompletedEvent(empty),
	)
	if !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("err = %v, want ErrInvalidMessage", err)
	}
}

func TestStreamReconcilesTerminalContent(t *testing.T) {
	final := messageItem("msg_1", "hello")
	response := responseFixture([]any{final}, "completed")

	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, messageItem("msg_1", "")),
			contentPartAddedEvent(2, 0, 0, "msg_1", ""),
			textDeltaEvent(3, 0, 0, "msg_1", "hello"),
			responseCompletedEvent(response),
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := llmtest.Collect(t, stream)
	if assembled.FinishReason != llm.FinishReasonStop {
		t.Fatalf("finish = %q", assembled.FinishReason)
	}
	if len(assembled.Message.Content) != 1 {
		t.Fatalf("content = %+v", assembled.Message.Content)
	}
	text, ok := assembled.Message.Content[0].(*llm.TextPart)
	if !ok || text.Text != "hello" {
		t.Fatalf("part = %+v", assembled.Message.Content[0])
	}
}

func TestStreamReasoningFromTerminal(t *testing.T) {
	final := reasoningItem("rs_1", "summary", "encrypted-1", "completed")
	response := responseFixture([]any{final}, "completed")

	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, reasoningItem("rs_1", "", "", "in_progress")),
			responseCompletedEvent(response),
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := llmtest.Collect(t, stream)
	if len(assembled.Message.Content) != 1 {
		t.Fatalf("content = %+v", assembled.Message.Content)
	}
	reasoning, ok := assembled.Message.Content[0].(*llm.ReasoningPart)
	if !ok || len(reasoning.Summary) != 1 || reasoning.Summary[0] != "summary" {
		t.Fatalf("part = %+v", assembled.Message.Content[0])
	}
	state, err := stateOf(reasoning)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if state.ItemID != "rs_1" || state.EncryptedContent != "encrypted-1" {
		t.Fatalf("state = %+v", state)
	}
}

func TestStreamFunctionCallProviderStatus(t *testing.T) {
	final := functionCallItem("fc_1", "call_1", "lookup", `{}`, "completed")
	response := responseFixture([]any{final}, "completed")

	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			responseCreatedEvent(responseFixture(nil, "in_progress")),
			outputItemAddedEvent(1, 0, functionCallItem("fc_1", "call_1", "lookup", "", "in_progress")),
			outputItemDoneEvent(2, 0, final),
			responseCompletedEvent(response),
		)
	})

	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assembled := llmtest.Collect(t, stream)
	if len(assembled.Message.Content) != 1 {
		t.Fatalf("content = %+v", assembled.Message.Content)
	}
	call, ok := assembled.Message.Content[0].(*llm.ToolCallPart)
	if !ok {
		t.Fatalf("part = %T", assembled.Message.Content[0])
	}
	if call.Status != "" {
		t.Fatalf("canonical status = %q, want empty", call.Status)
	}
	state, err := stateOf(call)
	if err != nil {
		t.Fatalf("stateOf: %v", err)
	}
	if state.Status != "completed" {
		t.Fatalf("provider status = %q", state.Status)
	}
	if assembled.FinishReason != llm.FinishReasonTool {
		t.Fatalf("finish = %q", assembled.FinishReason)
	}
}

// streamErrorForEvents starts a streaming request that emits the events and
// returns the terminal streaming error.
func streamErrorForEvents(t *testing.T, events ...map[string]any) error {
	t.Helper()
	model := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, events...)
	})
	stream, err := model.Stream(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	for {
		_, err = stream.Recv()
		if err != nil {
			return err
		}
	}
}
