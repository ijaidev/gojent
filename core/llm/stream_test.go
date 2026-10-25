package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestSliceStream_Close(t *testing.T) {
	stream := llm.NewSliceStream(context.Background(), []llm.StreamEvent{
		llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "chunk"},
	})
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, llm.ErrStreamClosed) {
		t.Errorf("Recv = %v, want ErrStreamClosed", err)
	}
}

func TestChannelStream(t *testing.T) {
	ch := make(chan llm.StreamEvent, 2)
	errCh := make(chan error, 1)
	canceled := false
	cancel := func() { canceled = true }
	stream := llm.NewChannelStream(context.Background(), cancel, ch, errCh)

	ch <- llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "part1"}
	ch <- llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "part2"}
	close(ch)

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if delta := first.(llm.PartDelta); delta.Text != "part1" {
		t.Fatalf("first = %+v", delta)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if delta := second.(llm.PartDelta); delta.Text != "part2" {
		t.Fatalf("second = %+v", delta)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv = %v, want io.EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !canceled {
		t.Error("cancel was not called")
	}
}

func TestChannelStream_Error(t *testing.T) {
	ch := make(chan llm.StreamEvent)
	errCh := make(chan error, 1)
	testErr := errors.New("network failure")
	errCh <- testErr
	stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
	defer func() { _ = stream.Close() }()
	if _, err := stream.Recv(); !errors.Is(err, testErr) {
		t.Errorf("Recv = %v, want %v", err, testErr)
	}
}

func TestChannelStream_TerminalErrorSurvivesClosedChannel(t *testing.T) {
	testErr := errors.New("provider stream failed")
	for i := 0; i < 500; i++ {
		ch := make(chan llm.StreamEvent)
		errCh := make(chan error, 1)
		errCh <- testErr
		close(errCh)
		close(ch)
		stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
		_, err := stream.Recv()
		_ = stream.Close()
		if !errors.Is(err, testErr) {
			t.Fatalf("iteration %d: Recv = %v, want %v", i, err, testErr)
		}
	}
}

func TestChannelStream_CleanCloseReturnsEOF(t *testing.T) {
	ch := make(chan llm.StreamEvent)
	errCh := make(chan error, 1)
	close(errCh)
	close(ch)
	stream := llm.NewChannelStream(context.Background(), nil, ch, errCh)
	defer func() { _ = stream.Close() }()
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv = %v, want io.EOF", err)
	}
}

func TestAssembler_InterleavedParts(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	events := []llm.StreamEvent{
		llm.MessageStart{ID: "resp-1", Model: "test"},
		llm.PartStart{Index: 0, Part: llm.NewReasoningPart("")},
		llm.PartDelta{Index: 0, Kind: llm.PartTypeReasoning, Text: "plan "},
		llm.PartDelta{Index: 0, Kind: llm.PartTypeReasoning, Text: "first"},
		llm.PartStart{Index: 1, Part: llm.NewTextPart("")},
		llm.PartDelta{Index: 1, Kind: llm.PartTypeText, Text: "hello "},
		llm.PartDelta{Index: 1, Kind: llm.PartTypeText, Text: "world"},
		llm.PartStart{Index: 2, Part: llm.NewToolCallPart(llm.ToolCall{})},
		llm.PartDelta{Index: 2, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
			ItemID: "item-a", CallID: "call-a", Name: "lookup", InputFragment: `{"q":`,
		}},
		llm.PartDelta{Index: 2, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
			InputFragment: `"go"}`,
		}},
		llm.PartStart{Index: 3, Part: llm.NewToolCallPart(llm.ToolCall{})},
		llm.PartDelta{Index: 3, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
			CallID: "call-b", Name: "lookup", InputSnapshot: json.RawMessage(`{"q":"rust"}`),
		}},
		llm.PartEnd{Index: 0},
		llm.PartEnd{Index: 1},
		llm.PartEnd{Index: 2},
		llm.PartEnd{Index: 3},
		llm.MessageEnd{
			Status:          llm.MessageStatusCompleted,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "tool_use",
			Usage:           llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply(%T): %v", event, err)
		}
	}

	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if response.ID != "resp-1" || response.Model != "test" {
		t.Errorf("response = %+v", response)
	}
	if response.FinishReason != llm.FinishReasonTool || response.RawFinishReason != "tool_use" {
		t.Errorf("finish = %q / %q", response.FinishReason, response.RawFinishReason)
	}
	if response.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", response.Usage)
	}
	if len(response.Message.Content) != 4 {
		t.Fatalf("content = %d, want 4", len(response.Message.Content))
	}
	if got := response.Message.Content[0].(*llm.ReasoningPart).Text; got != "plan first" {
		t.Errorf("reasoning = %q", got)
	}
	if got := response.Message.Content[1].(*llm.TextPart).Text; got != "hello world" {
		t.Errorf("text = %q", got)
	}
	calls := response.Message.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].ItemID != "item-a" || calls[0].CallID != "call-a" || string(calls[0].Input) != `{"q":"go"}` {
		t.Errorf("call a = %+v", calls[0])
	}
	if calls[1].CallID != "call-b" || string(calls[1].Input) != `{"q":"rust"}` {
		t.Errorf("call b = %+v", calls[1])
	}
	if err := response.Message.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestAssembler_IndexZeroWithoutPartStart(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	if err := assembler.Apply(llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "hi"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := assembler.Apply(llm.MessageEnd{FinishReason: llm.FinishReasonStop}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(response.Message.Content) != 1 || response.Message.Text() != "hi" {
		t.Errorf("content = %+v", response.Message.Content)
	}
}

func TestAssembler_LatePartStartMergesToolCall(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	events := []llm.StreamEvent{
		llm.PartDelta{Index: 0, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
			CallID: "call-a", InputFragment: `{"q":`,
		}},
		llm.PartStart{Index: 0, Part: llm.NewToolCallPart(llm.ToolCall{Name: "lookup"})},
		llm.PartDelta{Index: 0, Kind: llm.PartTypeToolCall, Call: &llm.ToolCallDelta{
			InputFragment: `"go"}`,
		}},
		llm.MessageEnd{FinishReason: llm.FinishReasonTool},
	}
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply(%T): %v", event, err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	call := response.Message.ToolCalls()[0]
	if call.Name != "lookup" || call.CallID != "call-a" || string(call.Input) != `{"q":"go"}` {
		t.Errorf("call = %+v", call)
	}
}

func TestAssembler_ReplaceResetsText(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	events := []llm.StreamEvent{
		llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "hel"},
		llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Replace: true, Text: "hello"},
		llm.MessageEnd{},
	}
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := response.Message.Text(); got != "hello" {
		t.Errorf("text = %q", got)
	}
}

func TestAssembler_ProviderMetadataMerge(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	events := []llm.StreamEvent{
		llm.PartDelta{
			Index: 0, Kind: llm.PartTypeReasoning, Text: "thought",
			Provider: llm.ProviderData{
				"google":    json.RawMessage(`{"signature":"a"}`),
				"anthropic": json.RawMessage(`{"signature":"x"}`),
			},
		},
		llm.PartDelta{
			Index: 0, Kind: llm.PartTypeReasoning, Text: " more",
			Provider: llm.ProviderData{
				"google": json.RawMessage(`{"signature":"b"}`),
			},
		},
		llm.ProviderEvent{Provider: "google", Raw: json.RawMessage(`{"trace":1}`)},
		llm.MessageEnd{},
	}
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply(%T): %v", event, err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reasoning := response.Message.Content[0].(*llm.ReasoningPart)
	if string(reasoning.Provider["google"]) != `{"signature":"b"}` {
		t.Errorf("google = %s", reasoning.Provider["google"])
	}
	if string(reasoning.Provider["anthropic"]) != `{"signature":"x"}` {
		t.Errorf("anthropic = %s", reasoning.Provider["anthropic"])
	}
	if string(response.Message.Provider["google"]) != `{"trace":1}` {
		t.Errorf("message provider = %s", response.Message.Provider["google"])
	}
}

func TestAssembler_Errors(t *testing.T) {
	tests := []struct {
		name   string
		events []llm.StreamEvent
	}{
		{
			name: "duplicate part start",
			events: []llm.StreamEvent{
				llm.PartStart{Index: 0, Part: llm.NewTextPart("a")},
				llm.PartStart{Index: 0, Part: llm.NewTextPart("b")},
			},
		},
		{
			name: "negative index",
			events: []llm.StreamEvent{
				llm.PartDelta{Index: -1, Kind: llm.PartTypeText, Text: "x"},
			},
		},
		{
			name: "delta after part end",
			events: []llm.StreamEvent{
				llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "a"},
				llm.PartEnd{Index: 0},
				llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "b"},
			},
		},
		{
			name: "kind mismatch",
			events: []llm.StreamEvent{
				llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "a"},
				llm.PartDelta{Index: 0, Kind: llm.PartTypeReasoning, Text: "b"},
			},
		},
		{
			name: "part end unknown index",
			events: []llm.StreamEvent{
				llm.PartEnd{Index: 2},
			},
		},
		{
			name: "event after end",
			events: []llm.StreamEvent{
				llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "a"},
				llm.MessageEnd{},
				llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "b"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembler := llm.NewAssembler(llm.RoleAssistant)
			var err error
			for _, event := range tt.events {
				if err = assembler.Apply(event); err != nil {
					break
				}
			}
			if err == nil {
				t.Fatal("Apply succeeded, want error")
			}
		})
	}
}

func TestAssembler_FinishRequiresMessageEnd(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	if err := assembler.Apply(llm.PartDelta{Index: 0, Kind: llm.PartTypeText, Text: "a"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := assembler.Finish(); !errors.Is(err, llm.ErrInvalidMessage) {
		t.Fatalf("Finish = %v, want ErrInvalidMessage", err)
	}
}

func TestAssembler_FinishSkipsEmptyParts(t *testing.T) {
	assembler := llm.NewAssembler(llm.RoleAssistant)
	events := []llm.StreamEvent{
		llm.PartStart{Index: 0, Part: llm.NewTextPart("")},
		llm.PartDelta{Index: 1, Kind: llm.PartTypeText, Text: "kept"},
		llm.MessageEnd{},
	}
	for _, event := range events {
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(response.Message.Content) != 1 || response.Message.Text() != "kept" {
		t.Errorf("content = %+v", response.Message.Content)
	}
}
