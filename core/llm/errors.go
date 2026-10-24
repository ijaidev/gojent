package llm

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrUnsupportedPart is returned when a provider does not support a given
	// message part.
	ErrUnsupportedPart = errors.New("llm: unsupported part")
	// ErrUnsupportedPartType is returned when a provider does not support the
	// part type (for example video or audio).
	ErrUnsupportedPartType = errors.New("llm: unsupported part type")
	// ErrUnsupportedMIMEType is returned when a provider does not support the
	// MIME type of a part.
	ErrUnsupportedMIMEType = errors.New("llm: unsupported mime type")
	// ErrInvalidPart is returned when a part is missing required data, a URI,
	// or text.
	ErrInvalidPart = errors.New("llm: invalid part")
)

// Kind classifies a model failure. Callers should branch on Kind, never on
// provider error strings.
type Kind string

const (
	// KindAuth means credentials are missing or invalid.
	KindAuth Kind = "auth"
	// KindRateLimit means the provider asked the caller to slow down.
	KindRateLimit Kind = "rate_limit"
	// KindOverloaded means the provider is temporarily unavailable.
	KindOverloaded Kind = "overloaded"
	// KindInvalid means the request or model output was invalid.
	KindInvalid Kind = "invalid"
	// KindContextOverflow means the conversation exceeded the context window.
	KindContextOverflow Kind = "context_overflow"
	// KindSafety means the provider blocked the request or response.
	KindSafety Kind = "safety"
	// KindTimeout means the request timed out.
	KindTimeout Kind = "timeout"
	// KindCanceled means the caller canceled the request.
	KindCanceled Kind = "canceled"
	// KindProvider is an unclassified provider failure.
	KindProvider Kind = "provider"
)

// Error is the provider-neutral error type returned by adapters.
type Error struct {
	// Kind classifies the failure.
	Kind Kind
	// RetryAfter is the delay requested by the provider, if any.
	RetryAfter time.Duration
	// StatusCode is the HTTP status code when one is available.
	StatusCode int
	// Err is the underlying provider error.
	Err error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := fmt.Sprintf("llm: %s", e.Kind)
	if e.StatusCode != 0 {
		msg += fmt.Sprintf(" (status %d)", e.StatusCode)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap returns the underlying provider error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable reports whether the failure is transient and safe for the runner
// to retry.
func (e *Error) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case KindRateLimit, KindOverloaded, KindTimeout:
		return true
	default:
		return false
	}
}

// AsError extracts an *Error from err, if present. A typed-nil *Error is
// reported as absent so callers can safely dereference the result.
func AsError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) && target != nil {
		return target, true
	}
	return nil, false
}
