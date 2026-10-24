package agent

import (
	"errors"
	"time"
)

// DefaultMaxSteps is the step budget used when Budget.MaxSteps is zero.
const DefaultMaxSteps = 8

// errDurationBudget identifies the run's own duration deadline so it can be
// distinguished from a caller's context deadline.
var errDurationBudget = errors.New("agent: duration budget exceeded")

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
