// Package agent provides the agent runtime: one streaming-first execution
// loop, typed tools, provider-neutral models, optional sessions, policy and
// event seams.
package agent

import (
	"errors"
	"fmt"
)

var (
	// ErrAgentRequired is returned when a nil agent is run.
	ErrAgentRequired = errors.New("agent: agent is required")
	// ErrModelRequired is returned when an agent has no model.
	ErrModelRequired = errors.New("agent: model is required")
	// ErrNameRequired is returned when an agent has no name.
	ErrNameRequired = errors.New("agent: name is required")
	// ErrDuplicateTool is returned when two tools share a name.
	ErrDuplicateTool = errors.New("agent: duplicate tool name")
	// ErrToolNil is returned when a tool list contains a typed nil.
	ErrToolNil = errors.New("agent: tool is nil")
	// ErrToolNameRequired is returned when a tool spec has no name.
	ErrToolNameRequired = errors.New("agent: tool name is required")
	// ErrSessionRequired is returned when a store is configured without a
	// session.
	ErrSessionRequired = errors.New("agent: session is required when a store is configured")
	// ErrSessionIDRequired is returned when a session has no ID.
	ErrSessionIDRequired = errors.New("agent: session id is required")
	// ErrMaxSteps is returned with a partial response when the run uses its
	// full step budget.
	ErrMaxSteps = errors.New("agent: max steps exceeded")
	// ErrBudgetExceeded is returned when a run exceeds its token, call, or
	// time budget.
	ErrBudgetExceeded = errors.New("agent: budget exceeded")
	// ErrTerminated is returned when a policy terminates a run.
	ErrTerminated = errors.New("agent: run terminated by policy")
	// ErrToolDenied is returned internally when a tool call is denied.
	ErrToolDenied = errors.New("agent: tool call denied")
	// ErrOutputSchema is returned when a structured output schema cannot be
	// generated from the requested type.
	ErrOutputSchema = errors.New("agent: invalid output schema")
	// ErrResponseRequired is returned when a model or policy produces no
	// response.
	ErrResponseRequired = errors.New("agent: response is required")
	// ErrRunCanceled is returned when a run is canceled without a more
	// specific cause.
	ErrRunCanceled = errors.New("agent: run canceled")
)

// PanicError converts a recovered panic into an error.
type PanicError struct {
	Value any
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	return fmt.Sprintf("agent: panic: %v", e.Value)
}

// RecoveryError renders a panic as a provider-safe tool result.
func recoveryError(err error) string {
	var panicErr *PanicError
	if errors.As(err, &panicErr) {
		return fmt.Sprintf("panic: %v", panicErr.Value)
	}
	return err.Error()
}

// toolErrorPayload renders an error as the JSON object delivered to a model.
func toolErrorPayload(err error) string {
	if err == nil {
		return `{"error":"unknown error"}`
	}
	encoded, marshalErr := marshalJSON(map[string]string{"error": recoveryError(err)})
	if marshalErr != nil {
		return `{"error":"tool failed"}`
	}
	return encoded
}
