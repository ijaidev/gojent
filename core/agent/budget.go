package agent

import (
	"time"
)

// DefaultMaxSteps is the step budget used when Budget.MaxSteps is zero.
const DefaultMaxSteps = 8

// DefaultToolTimeout bounds a single tool execution when
// RunOptions.ToolTimeout is zero.
const DefaultToolTimeout = 2 * time.Minute

// Budget bounds a run. Zero values mean "use the default" for MaxSteps and
// "unlimited" for the other fields.
type Budget struct {
	// MaxSteps is the maximum number of model calls. Zero uses
	// DefaultMaxSteps.
	MaxSteps int
	// MaxToolCalls is the maximum number of tool executions. Zero is
	// unlimited.
	MaxToolCalls int
	// MaxTotalTokens is the maximum cumulative token usage. Zero is unlimited.
	MaxTotalTokens int
	// MaxDuration bounds wall-clock execution. Zero is unlimited.
	MaxDuration time.Duration
}

// steps returns the effective step limit.
func (b Budget) steps() int {
	if b.MaxSteps <= 0 {
		return DefaultMaxSteps
	}
	return b.MaxSteps
}

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
