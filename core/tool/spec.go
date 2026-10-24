package tool

import "encoding/json"

// ExecutionMode is a tool's scheduling preference.
//
// The runner currently executes every call sequentially, so the mode is
// advisory metadata for schedulers that can parallelize safely.
type ExecutionMode string

const (
	// ExecutionParallel marks a tool as safe to run alongside other calls from
	// the same assistant message.
	ExecutionParallel ExecutionMode = "parallel"
	// ExecutionSequential requires a scheduler to finish this call before
	// starting the next one.
	ExecutionSequential ExecutionMode = "sequential"
)

// Annotations describes a tool's side effects.
type Annotations struct {
	// ReadOnly means the tool does not mutate state.
	ReadOnly bool
	// Destructive means the tool may delete or overwrite data.
	Destructive bool
	// Idempotent means repeating the same call has the same effect.
	Idempotent bool
	// OpenWorld means the tool interacts with an unbounded external system.
	OpenWorld bool
}

// Spec is the immutable description of a tool.
type Spec struct {
	// Name is the unique tool name exposed to the model.
	Name string
	// Description explains what the tool does and when to use it.
	Description string
	// InputSchema is the JSON Schema for the tool input.
	InputSchema map[string]any
	// Annotations describes side effects.
	Annotations Annotations
	// ExecutionMode declares the tool's scheduling preference.
	ExecutionMode ExecutionMode
	// Strict asks the provider to enforce the input schema exactly.
	Strict bool
}

// Call is one model-requested tool invocation.
type Call struct {
	// ID is the unique call identifier. The runner guarantees it is non-empty
	// and unique within a run.
	ID string
	// Index is the call position within the assistant message.
	Index int
	// Name is the requested tool name.
	Name string
	// Arguments is the validated JSON input.
	Arguments json.RawMessage
	// IdempotencyKey is stable across retries of the same call when the runner
	// can derive one.
	IdempotencyKey string
}
