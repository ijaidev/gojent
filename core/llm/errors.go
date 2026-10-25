package llm

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidMessage is returned when a message has an invalid role, empty
	// content, or content that is not allowed for its role.
	ErrInvalidMessage = errors.New("llm: invalid message")
	// ErrInvalidPart is returned when a part is missing required data or a
	// part cannot be used in the current position.
	ErrInvalidPart = errors.New("llm: invalid part")
	// ErrUnknownPartType is returned when JSON contains a part type this
	// version does not know. Decoding fails closed.
	ErrUnknownPartType = errors.New("llm: unknown part type")
	// ErrInvalidToolCall is returned when a tool call is missing its name,
	// correlation ID, or valid JSON input.
	ErrInvalidToolCall = errors.New("llm: invalid tool call")
	// ErrInvalidToolResult is returned when a tool result is missing its call
	// ID or contains content that is not allowed in a result.
	ErrInvalidToolResult = errors.New("llm: invalid tool result")
	// ErrUnsupported is returned when a provider cannot honor a request
	// feature. Adapters must return it instead of silently degrading.
	ErrUnsupported = errors.New("llm: unsupported feature")
	// ErrUnsupportedPart is returned when a provider does not support a given
	// message part.
	ErrUnsupportedPart = errors.New("llm: unsupported part")
	// ErrUnsupportedPartType is returned when a provider does not support the
	// part type (for example video or audio).
	ErrUnsupportedPartType = errors.New("llm: unsupported part type")
	// ErrUnsupportedMIMEType is returned when a provider does not support the
	// MIME type of a part.
	ErrUnsupportedMIMEType = errors.New("llm: unsupported mime type")
	// ErrNoProviderData is returned when provider metadata is absent or the
	// map was nil while writing.
	ErrNoProviderData = errors.New("llm: no provider data")
	// ErrUnsupportedFormatVersion is returned when persisted JSON was written
	// by an incompatible message-model version.
	ErrUnsupportedFormatVersion = errors.New("llm: unsupported format version")
	// ErrStreamClosed is returned when an operation is attempted on an
	// already closed stream.
	ErrStreamClosed = errors.New("llm: stream is closed")
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
