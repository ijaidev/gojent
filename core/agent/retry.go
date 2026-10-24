package agent

import (
	"context"
	"errors"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

// RetryPolicy configures retries of transient model failures.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the first. Zero
	// or one disables retries.
	MaxAttempts int
	// InitialDelay is the first backoff delay. Zero uses 500ms.
	InitialDelay time.Duration
	// MaxDelay caps the backoff delay. Zero uses 8s.
	MaxDelay time.Duration
}

// delay returns the backoff before the given attempt (1-based).
func (p RetryPolicy) delay(attempt int) time.Duration {
	base := p.InitialDelay
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	maximum := p.MaxDelay
	if maximum <= 0 {
		maximum = 8 * time.Second
	}
	delay := base
	for i := 1; i < attempt && delay < maximum; i++ {
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	return delay
}

func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if providerErr, ok := llm.AsError(err); ok {
		return providerErr.Retryable()
	}
	return false
}
