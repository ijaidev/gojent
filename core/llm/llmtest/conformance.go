// Package llmtest provides a shared conformance suite for llm.Model
// implementations.
//
// Provider adapters run Run to prove that generation and streaming produce the
// same canonical response, that usage and finish reasons survive streaming,
// and that canceled contexts are honored. The suite intentionally avoids
// provider-specific assertions; provider tests can add those separately.
package llmtest

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

// Factory returns a fresh model for one subtest. Implementations should
// register cleanup with t.Cleanup.
type Factory func(t *testing.T) llm.Model

// Case describes one deterministic request and its expected canonical output.
type Case struct {
	// Name identifies the subtest.
	Name string
	// Request is sent to both Generate and Stream. It is cloned by the suite.
	Request *llm.Request
	// Text is the expected assistant text. Empty means no text is asserted.
	Text string
	// Reasoning is the expected reasoning trace.
	Reasoning string
	// ToolCalls is the expected number of tool calls.
	ToolCalls int
	// FinishReason is the expected terminal reason for both paths.
	FinishReason llm.FinishReason
	// SkipUsageComparison allows providers that do not report usage while
	// streaming to opt out of the usage assertion.
	SkipUsageComparison bool
}

// Run executes the conformance suite against models produced by newModel.
func Run(t *testing.T, newModel Factory, cases ...Case) {
	t.Helper()

	t.Run("ContextCancellation", func(t *testing.T) {
		model := newModel(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := model.Generate(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
			t.Errorf("Generate with canceled context = %v, want context.Canceled", err)
		}
		if streamer, ok := model.(llm.Streamer); ok {
			if _, err := streamer.Stream(ctx, &llm.Request{}); !errors.Is(err, context.Canceled) {
				t.Errorf("Stream with canceled context = %v, want context.Canceled", err)
			}
		}
	})

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			runCase(t, newModel(t), tc)
		})
	}
}

func runCase(t *testing.T, model llm.Model, tc Case) {
	t.Helper()
	ctx := context.Background()

	generated, err := model.Generate(ctx, tc.Request.Clone())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	assertResponse(t, "generate", generated, tc)

	streamer, ok := model.(llm.Streamer)
	if !ok {
		return
	}
	stream, err := streamer.Stream(ctx, tc.Request.Clone())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	streamed := Collect(t, stream)
	assertResponse(t, "stream", streamed, tc)

	if generated.Message.Text() != streamed.Message.Text() {
		t.Errorf("text mismatch: generate %q, stream %q", generated.Message.Text(), streamed.Message.Text())
	}
	if generatedReasoning := ReasoningText(generated.Message); generatedReasoning != ReasoningText(streamed.Message) {
		t.Errorf("reasoning mismatch: generate %q, stream %q", generatedReasoning, ReasoningText(streamed.Message))
	}
	if generated.FinishReason != streamed.FinishReason {
		t.Errorf("finish reason mismatch: generate %q, stream %q", generated.FinishReason, streamed.FinishReason)
	}
	if !tc.SkipUsageComparison && generated.Usage != streamed.Usage {
		t.Errorf("usage mismatch: generate %+v, stream %+v", generated.Usage, streamed.Usage)
	}
	generatedCalls := generated.Message.ToolCalls()
	streamedCalls := streamed.Message.ToolCalls()
	if len(generatedCalls) != len(streamedCalls) {
		t.Fatalf("tool calls mismatch: generate %d, stream %d", len(generatedCalls), len(streamedCalls))
	}
	for i := range generatedCalls {
		want := generatedCalls[i]
		got := streamedCalls[i]
		if got.Name != want.Name {
			t.Errorf("tool call %d name: generate %q, stream %q", i, want.Name, got.Name)
		}
		if string(got.Input) != string(want.Input) {
			t.Errorf("tool call %d input: generate %s, stream %s", i, want.Input, got.Input)
		}
		if want.CallID != "" && got.CallID != "" && got.CallID != want.CallID {
			t.Errorf("tool call %d call id: generate %q, stream %q", i, want.CallID, got.CallID)
		}
	}
}

func assertResponse(t *testing.T, source string, response *llm.Response, tc Case) {
	t.Helper()
	if response == nil {
		t.Fatalf("%s returned a nil response", source)
	}
	if err := response.Message.Validate(); err != nil {
		t.Fatalf("%s message invalid: %v", source, err)
	}
	if response.Message.Role != llm.RoleAssistant {
		t.Errorf("%s role = %q, want assistant", source, response.Message.Role)
	}
	if tc.Text != "" && response.Message.Text() != tc.Text {
		t.Errorf("%s text = %q, want %q", source, response.Message.Text(), tc.Text)
	}
	if got := ReasoningText(response.Message); got != tc.Reasoning {
		t.Errorf("%s reasoning = %q, want %q", source, got, tc.Reasoning)
	}
	calls := response.Message.ToolCalls()
	if len(calls) != tc.ToolCalls {
		t.Errorf("%s tool calls = %d, want %d", source, len(calls), tc.ToolCalls)
	}
	for i, call := range calls {
		if call.Name == "" {
			t.Errorf("%s tool call %d has no name", source, i)
		}
		if call.CallID == "" {
			t.Errorf("%s tool call %d has no call id", source, i)
		}
	}
	if tc.FinishReason != "" && response.FinishReason != tc.FinishReason {
		t.Errorf("%s finish reason = %q, want %q", source, response.FinishReason, tc.FinishReason)
	}
}

// ReasoningText joins readable reasoning and summaries in part order.
func ReasoningText(message llm.Message) string {
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

// Collect drains a stream and assembles its canonical response. It fails the
// test on receive or assembly errors.
func Collect(t *testing.T, stream llm.Stream) *llm.Response {
	t.Helper()
	defer func() {
		if err := stream.Close(); err != nil && !errors.Is(err, llm.ErrStreamClosed) {
			t.Errorf("Close: %v", err)
		}
	}()

	assembler := llm.NewAssembler(llm.RoleAssistant)
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if event == nil {
			continue
		}
		if err := assembler.Apply(event); err != nil {
			t.Fatalf("Apply(%T): %v", event, err)
		}
	}
	response, err := assembler.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return response
}
