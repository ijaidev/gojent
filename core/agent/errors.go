package agent

import "errors"

var (
	// ErrModelRequired is returned when an Agent is created without an LLM model.
	ErrModelRequired = errors.New("agent: model is required")
	// ErrIDRequired is returned when an Agent is created without an ID.
	ErrIDRequired = errors.New("agent: id is required")
	// ErrToolNil is returned when attempting to add a nil tool to an Agent.
	ErrToolNil = errors.New("agent: cannot add nil tool")
	// ErrStreamNotFinished is returned when accessing the final agent response before the stream completes.
	ErrStreamNotFinished = errors.New("agent: stream has not finished yet")
	// ErrStreamClosed is returned when operations are attempted on a closed stream.
	ErrStreamClosed = errors.New("agent: stream is closed")
)
