// Package llmtest provides a shared conformance suite for llm.Model
// implementations.
//
// Provider adapters run Run to prove that generation and streaming produce the
// same canonical response, that usage and finish reasons survive streaming,
// and that canceled contexts are honored. The suite intentionally avoids
// provider-specific assertions; provider tests can add those separately.
package llmtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
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
	// Model optionally overrides the suite factory for this case, which lets
	// one suite cover differently configured models.
	Model func(t *testing.T) llm.Model
	// Text is the expected assistant text. Empty means no text is asserted.
	Text string
	// Reasoning is the expected reasoning trace.
	Reasoning string
	// ToolCalls is the expected number of tool calls.
	ToolCalls int
	// Usage is the expected terminal usage. Nil means no usage is asserted.
	Usage *llm.Usage
	// FinishReason is the expected terminal reason for both paths.
	FinishReason llm.FinishReason
	// RawFinishReason is the expected provider reason string. Empty means no
	// assertion.
	RawFinishReason string
	// Status is the expected message status. Empty means no assertion.
	Status llm.MessageStatus
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

	for index := range cases {
		tc := &cases[index]
		t.Run(tc.Name, func(t *testing.T) {
			model := newModel
			if tc.Model != nil {
				model = tc.Model
			}
			RunCase(t, model(t), *tc)
		})
	}
}

// RunCase runs one conformance case against an existing model.
func RunCase(t *testing.T, model llm.Model, tc Case) {
	t.Helper()
	runCase(t, model, tc)
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

	compareResponses(t, generated, streamed, tc.SkipUsageComparison)
}

// compareResponses proves Generate and Stream produce the same canonical
// message. Provider metadata is compared semantically so adapter-owned keys
// survive streaming without the suite knowing them.
func compareResponses(t *testing.T, generated, streamed *llm.Response, skipUsage bool) {
	t.Helper()
	compareMessages(t, generated.Message, streamed.Message)

	if generated.FinishReason != streamed.FinishReason {
		t.Errorf("finish reason mismatch: generate %q, stream %q", generated.FinishReason, streamed.FinishReason)
	}
	if generated.RawFinishReason != streamed.RawFinishReason {
		t.Errorf("raw finish reason mismatch: generate %q, stream %q", generated.RawFinishReason, streamed.RawFinishReason)
	}
	if generated.Status != streamed.Status {
		t.Errorf("status mismatch: generate %q, stream %q", generated.Status, streamed.Status)
	}
	if !skipUsage && generated.Usage != streamed.Usage {
		t.Errorf("usage mismatch: generate %+v, stream %+v", generated.Usage, streamed.Usage)
	}
	if !providerDataEqual(generated.Provider, streamed.Provider) {
		t.Errorf("message provider metadata mismatch: generate %s, stream %s", marshalProvider(generated.Provider), marshalProvider(streamed.Provider))
	}
}

func compareMessages(t *testing.T, generated, streamed llm.Message) {
	t.Helper()
	if generated.Role != streamed.Role {
		t.Errorf("role mismatch: generate %q, stream %q", generated.Role, streamed.Role)
	}
	if generated.Status != streamed.Status {
		t.Errorf("message status mismatch: generate %q, stream %q", generated.Status, streamed.Status)
	}
	if len(generated.Content) != len(streamed.Content) {
		t.Fatalf("part count mismatch: generate %d, stream %d", len(generated.Content), len(streamed.Content))
	}
	for index := range generated.Content {
		comparePart(t, index, generated.Content[index], streamed.Content[index])
	}
}

func comparePart(t *testing.T, index int, generated, streamed llm.Part) {
	t.Helper()
	if generated.PartType() != streamed.PartType() {
		t.Fatalf("part %d type mismatch: generate %q, stream %q", index, generated.PartType(), streamed.PartType())
	}
	if !providerDataEqual(generated.Meta().Provider, streamed.Meta().Provider) {
		t.Errorf("part %d provider metadata mismatch: generate %s, stream %s",
			index, marshalProvider(generated.Meta().Provider), marshalProvider(streamed.Meta().Provider))
	}

	switch got := generated.(type) {
	case *llm.TextPart:
		want := streamed.(*llm.TextPart)
		if got.Text != want.Text {
			t.Errorf("part %d text mismatch: generate %q, stream %q", index, got.Text, want.Text)
		}
	case *llm.ReasoningPart:
		want := streamed.(*llm.ReasoningPart)
		if got.Text != want.Text || got.Redacted != want.Redacted {
			t.Errorf("part %d reasoning mismatch: generate %+v, stream %+v", index, got, want)
		}
		if !reflect.DeepEqual(got.Summary, want.Summary) {
			t.Errorf("part %d reasoning summary mismatch: generate %v, stream %v", index, got.Summary, want.Summary)
		}
	case *llm.FilePart:
		want := streamed.(*llm.FilePart)
		if got.MIMEType != want.MIMEType || got.URL != want.URL || got.FileID != want.FileID ||
			got.Filename != want.Filename || got.Detail != want.Detail || !bytes.Equal(got.Data, want.Data) {
			t.Errorf("part %d file mismatch: generate %+v, stream %+v", index, got, want)
		}
	case *llm.ToolCallPart:
		want := streamed.(*llm.ToolCallPart)
		if got.ItemID != want.ItemID || got.CallID != want.CallID || got.Name != want.Name ||
			got.EffectiveKind() != want.EffectiveKind() || got.Status != want.Status {
			t.Errorf("part %d tool call mismatch: generate %+v, stream %+v", index, got.ToolCall, want.ToolCall)
		}
		if !jsonEqual(got.Input, want.Input) {
			t.Errorf("part %d tool input mismatch: generate %s, stream %s", index, got.Input, want.Input)
		}
	case *llm.ToolResultPart:
		want := streamed.(*llm.ToolResultPart)
		if got.CallID != want.CallID || got.Name != want.Name ||
			got.EffectiveKind() != want.EffectiveKind() || got.Status != want.Status {
			t.Errorf("part %d tool result mismatch: generate %+v, stream %+v", index, got.ToolResult, want.ToolResult)
		}
		if !jsonEqual(got.Structured, want.Structured) || !jsonEqual(got.InputRequests, want.InputRequests) {
			t.Errorf("part %d tool result payload mismatch: generate %+v, stream %+v", index, got.ToolResult, want.ToolResult)
		}
		if len(got.Content) != len(want.Content) {
			t.Errorf("part %d result content count mismatch: generate %d, stream %d", index, len(got.Content), len(want.Content))
			return
		}
		for contentIndex := range got.Content {
			comparePart(t, contentIndex, got.Content[contentIndex], want.Content[contentIndex])
		}
	case *llm.RefusalPart:
		want := streamed.(*llm.RefusalPart)
		if got.Text != want.Text {
			t.Errorf("part %d refusal mismatch: generate %q, stream %q", index, got.Text, want.Text)
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
	if tc.RawFinishReason != "" && response.RawFinishReason != tc.RawFinishReason {
		t.Errorf("%s raw finish reason = %q, want %q", source, response.RawFinishReason, tc.RawFinishReason)
	}
	if tc.Status != "" && response.Status != tc.Status {
		t.Errorf("%s status = %q, want %q", source, response.Status, tc.Status)
	}
	if tc.Usage != nil && response.Usage != *tc.Usage {
		t.Errorf("%s usage = %+v, want %+v", source, response.Usage, *tc.Usage)
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

func providerDataEqual(a, b llm.ProviderData) bool {
	if len(a) != len(b) {
		return false
	}
	for key, left := range a {
		right, ok := b[key]
		if !ok || !jsonEqual(left, right) {
			return false
		}
	}
	return true
}

func jsonEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &right); err != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func marshalProvider(data llm.ProviderData) string {
	if len(data) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return "<invalid>"
	}
	return string(encoded)
}
