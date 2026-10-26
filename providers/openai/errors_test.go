package openai

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	sdk "github.com/openai/openai-go"
)

func TestClassifyStatus(t *testing.T) {
	tests := map[int]llm.Kind{
		400: llm.KindInvalid,
		401: llm.KindAuth,
		403: llm.KindAuth,
		404: llm.KindInvalid,
		409: llm.KindInvalid,
		413: llm.KindContextOverflow,
		422: llm.KindInvalid,
		429: llm.KindRateLimit,
		500: llm.KindProvider,
		503: llm.KindOverloaded,
		504: llm.KindTimeout,
	}
	for status, want := range tests {
		if got := classifyStatus(status); got != want {
			t.Errorf("status %d = %q, want %q", status, got, want)
		}
	}
}

func TestClassifyCode(t *testing.T) {
	tests := map[string]llm.Kind{
		"invalid_prompt":                 llm.KindInvalid,
		"invalid_image":                  llm.KindInvalid,
		"image_too_large":                llm.KindInvalid,
		"rate_limit_exceeded":            llm.KindRateLimit,
		"context_length_exceeded":        llm.KindContextOverflow,
		"content_policy_violation":       llm.KindSafety,
		"image_content_policy_violation": llm.KindSafety,
		"server_error":                   llm.KindProvider,
		"":                               llm.KindProvider,
	}
	for code, want := range tests {
		if got := classifyCode(code); got != want {
			t.Errorf("code %q = %q, want %q", code, got, want)
		}
	}
}

func TestWrapError(t *testing.T) {
	apiErr := &sdk.Error{StatusCode: 429}
	wrapped := wrapError(apiErr)
	var llmErr *llm.Error
	if !errors.As(wrapped, &llmErr) {
		t.Fatalf("wrapped = %T, want *llm.Error", wrapped)
	}
	if llmErr.Kind != llm.KindRateLimit || llmErr.StatusCode != 429 {
		t.Fatalf("error = %+v", llmErr)
	}

	for _, test := range []struct {
		name string
		err  error
		kind llm.Kind
	}{
		{name: "deadline", err: context.DeadlineExceeded, kind: llm.KindTimeout},
		{name: "canceled", err: context.Canceled, kind: llm.KindCanceled},
	} {
		wrapped := wrapError(test.err)
		if !errors.As(wrapped, &llmErr) {
			t.Fatalf("%s: wrapped = %T, want *llm.Error", test.name, wrapped)
		}
		if llmErr.Kind != test.kind {
			t.Fatalf("%s: kind = %q, want %q", test.name, llmErr.Kind, test.kind)
		}
	}

	plain := errors.New("network down")
	if got := wrapError(plain); !errors.Is(got, plain) {
		t.Fatalf("plain err = %v, want unchanged", got)
	}
	if got := wrapError(nil); got != nil {
		t.Fatalf("nil err = %v", got)
	}
}

func TestStreamErrorClassifiesCode(t *testing.T) {
	err := streamError("context_length_exceeded", "too long")
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) {
		t.Fatalf("err = %T, want *llm.Error", err)
	}
	if llmErr.Kind != llm.KindContextOverflow {
		t.Fatalf("kind = %q", llmErr.Kind)
	}
}
