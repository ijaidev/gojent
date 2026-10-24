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
	"sort"
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
	// Thinking is the expected reasoning trace.
	Thinking string
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
		tc := tc
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
	if generated.Message.Thinking != streamed.Message.Thinking {
		t.Errorf("thinking mismatch: generate %q, stream %q", generated.Message.Thinking, streamed.Message.Thinking)
	}
	if generated.FinishReason != streamed.FinishReason {
		t.Errorf("finish reason mismatch: generate %q, stream %q", generated.FinishReason, streamed.FinishReason)
	}
	if !tc.SkipUsageComparison && generated.Usage != streamed.Usage {
		t.Errorf("usage mismatch: generate %+v, stream %+v", generated.Usage, streamed.Usage)
	}
	if len(generated.Message.ToolCalls) != len(streamed.Message.ToolCalls) {
		t.Fatalf("tool calls mismatch: generate %d, stream %d", len(generated.Message.ToolCalls), len(streamed.Message.ToolCalls))
	}
	for i := range generated.Message.ToolCalls {
		want := generated.Message.ToolCalls[i]
		got := streamed.Message.ToolCalls[i]
		if got.Name != want.Name {
			t.Errorf("tool call %d name: generate %q, stream %q", i, want.Name, got.Name)
		}
		if got.Arguments != want.Arguments {
			t.Errorf("tool call %d arguments: generate %q, stream %q", i, want.Arguments, got.Arguments)
		}
		if want.ID != "" && got.ID != "" && got.ID != want.ID {
			t.Errorf("tool call %d id: generate %q, stream %q", i, want.ID, got.ID)
		}
	}
}

func assertResponse(t *testing.T, source string, response *llm.Response, tc Case) {
	t.Helper()
	if response == nil {
		t.Fatalf("%s returned a nil response", source)
	}
	if response.Message.Role != llm.RoleAssistant {
		t.Errorf("%s role = %q, want assistant", source, response.Message.Role)
	}
	if tc.Text != "" && response.Message.Text() != tc.Text {
		t.Errorf("%s text = %q, want %q", source, response.Message.Text(), tc.Text)
	}
	if response.Message.Thinking != tc.Thinking {
		t.Errorf("%s thinking = %q, want %q", source, response.Message.Thinking, tc.Thinking)
	}
	if len(response.Message.ToolCalls) != tc.ToolCalls {
		t.Errorf("%s tool calls = %d, want %d", source, len(response.Message.ToolCalls), tc.ToolCalls)
	}
	for i, call := range response.Message.ToolCalls {
		if call.Index != i {
			t.Errorf("%s tool call %d index = %d, want %d", source, i, call.Index, i)
		}
		if call.Name == "" {
			t.Errorf("%s tool call %d has no name", source, i)
		}
	}
	if tc.FinishReason != "" && response.FinishReason != tc.FinishReason {
		t.Errorf("%s finish reason = %q, want %q", source, response.FinishReason, tc.FinishReason)
	}
}

// Collect drains a stream and assembles its canonical response. It fails the
// test on receive errors.
func Collect(t *testing.T, stream llm.Stream) *llm.Response {
	t.Helper()
	defer func() {
		if err := stream.Close(); err != nil && !errors.Is(err, llm.ErrStreamClosed) {
			t.Errorf("Close: %v", err)
		}
	}()

	response := &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}}
	var text, thinking string
	calls := make(map[int]*llm.ToolCall)
	var order []int

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if chunk == nil {
			continue
		}
		text += chunk.Delta
		thinking += chunk.ThinkingDelta
		if chunk.FinishReason != "" {
			response.FinishReason = chunk.FinishReason
		}
		if chunk.Usage != nil {
			response.Usage = response.Usage.Add(*chunk.Usage)
		}
		if chunk.ProviderData != nil {
			response.Message.ProviderData = chunk.ProviderData
		}
		for _, fragment := range chunk.ToolCalls {
			index := fragment.Index
			call, exists := calls[index]
			if !exists {
				copied := fragment
				calls[index] = &copied
				order = append(order, index)
				continue
			}
			if fragment.ID != "" {
				call.ID = fragment.ID
			}
			if fragment.Name != "" {
				call.Name = fragment.Name
			}
			call.Arguments += fragment.Arguments
		}
	}

	if text != "" {
		response.Message.Content = []llm.Part{llm.TextPart(text)}
	}
	response.Message.Thinking = thinking
	sort.Ints(order)
	for _, index := range order {
		call := calls[index]
		call.Index = len(response.Message.ToolCalls)
		response.Message.ToolCalls = append(response.Message.ToolCalls, *call)
	}
	return response
}
