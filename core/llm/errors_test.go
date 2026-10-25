package llm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestAsError(t *testing.T) {
	providerErr := &llm.Error{Kind: llm.KindRateLimit, RetryAfter: 2}
	wrapped := fmt.Errorf("adapter: %w", providerErr)
	if got, ok := llm.AsError(wrapped); !ok || got != providerErr {
		t.Fatalf("AsError(%v) = %v, %v", wrapped, got, ok)
	}

	if got, ok := llm.AsError(errors.New("plain")); ok || got != nil {
		t.Fatalf("AsError(plain) = %v, %v; want nil, false", got, ok)
	}
}

func TestAsError_TypedNil(t *testing.T) {
	var typedNil *llm.Error
	var err error = typedNil

	got, ok := llm.AsError(err)
	if ok || got != nil {
		t.Fatalf("AsError(typed nil) = %v, %v; want nil, false", got, ok)
	}
	if got, ok := llm.AsError(fmt.Errorf("wrapped: %w", err)); ok || got != nil {
		t.Fatalf("AsError(wrapped typed nil) = %v, %v; want nil, false", got, ok)
	}
}

type testStreamer struct{}

func (testStreamer) ModelID() string { return "test" }

func (testStreamer) Info() llm.Info { return llm.Info{Provider: "test", Model: "test"} }

func (testStreamer) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, nil
}

func (testStreamer) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	return llm.NewSliceStream(context.Background(), nil), nil
}

// Streamer must include Model: a streamer that cannot generate is not a
// valid llm.Streamer.
var _ llm.Streamer = testStreamer{}
