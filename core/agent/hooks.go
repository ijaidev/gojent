package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Hooks defines optional lifecycle callbacks invoked during agent execution.
// Every field is optional; nil callbacks are skipped. Hooks enable logging,
// tracing, metrics, cost accounting, guardrails, and human approval gates
// without wrapping the underlying model.
//
// Hooks can be configured on an Agent and overridden for a single run with
// WithHooks. When both are set, non-nil per-call callbacks take precedence
// field by field.
type Hooks struct {
	// BeforeModel is called before each model generation step, once per ReAct
	// iteration. The event's Request may be mutated to adjust the outgoing
	// request (e.g. exchanging the model, injecting messages, or overriding
	// parameters). Returning an error aborts the run with that error and skips
	// the model call.
	BeforeModel func(ctx context.Context, ev *ModelEvent) error

	// AfterModel is called after each model generation step completes,
	// including when the step fails. The event's Err field is non-nil on
	// failure and Response is nil when no output was produced.
	AfterModel func(ctx context.Context, ev *ModelEvent)

	// BeforeTool is called before a tool is executed. The event's Call may be
	// mutated to adjust the invocation. Returning an error skips execution and
	// delivers the error to the model as the tool output, allowing the model to
	// recover; the run itself continues.
	BeforeTool func(ctx context.Context, ev *ToolEvent) error

	// AfterTool is called after a tool invocation finishes, including when it
	// was skipped by BeforeTool or returned an error.
	AfterTool func(ctx context.Context, ev *ToolEvent)

	// OnStorageError is called when a persistence operation fails during a run,
	// reporting the operation name (e.g. "save_messages", "list_messages").
	// Storage failures never abort generation.
	OnStorageError func(ctx context.Context, op string, err error)
}

// ModelEvent describes a single model generation step within the ReAct loop.
type ModelEvent struct {
	// Step is the zero-based ReAct iteration index.
	Step int
	// Request is the request sent to the model. It may be mutated in BeforeModel,
	// in which case the mutated request is used for the step.
	Request *llm.GenerateRequest
	// Response is the model response for the step. Populated for AfterModel,
	// unless the step failed before producing output.
	Response *llm.GenerateResponse
	// Err is the model or stream error for the step, if any.
	Err error
}

// ToolEvent describes a single tool invocation within the ReAct loop.
type ToolEvent struct {
	// Step is the zero-based ReAct iteration index in which the tool was requested.
	Step int
	// Call is the tool invocation. It may be mutated in BeforeTool, in which case
	// the mutated invocation is executed.
	Call *llm.ToolCall
	// Output is the result delivered to the model as the tool message.
	Output string
	// Err is the tool execution error, or the BeforeTool error when skipped.
	Err error
	// Skipped reports that execution was skipped because BeforeTool returned an error.
	Skipped bool
}

// Storage operation names reported through OnStorageError.
const (
	opGetThread    = "get_thread"
	opSaveThread   = "save_thread"
	opListMessages = "list_messages"
	opSaveMessages = "save_messages"
)

// mergeHooks layers override callbacks over base callbacks field by field.
// Non-nil override callbacks take precedence; nil fields fall back to base.
func mergeHooks(base, override Hooks) Hooks {
	merged := base
	if override.BeforeModel != nil {
		merged.BeforeModel = override.BeforeModel
	}
	if override.AfterModel != nil {
		merged.AfterModel = override.AfterModel
	}
	if override.BeforeTool != nil {
		merged.BeforeTool = override.BeforeTool
	}
	if override.AfterTool != nil {
		merged.AfterTool = override.AfterTool
	}
	if override.OnStorageError != nil {
		merged.OnStorageError = override.OnStorageError
	}
	return merged
}

// beforeModel notifies the BeforeModel hook, if configured.
func (h Hooks) beforeModel(ctx context.Context, step int, req *llm.GenerateRequest) error {
	if h.BeforeModel == nil {
		return nil
	}
	return h.BeforeModel(ctx, &ModelEvent{Step: step, Request: req})
}

// afterModel notifies the AfterModel hook, if configured.
func (h Hooks) afterModel(ctx context.Context, step int, req *llm.GenerateRequest, resp *llm.GenerateResponse, err error) {
	if h.AfterModel == nil {
		return
	}
	h.AfterModel(ctx, &ModelEvent{Step: step, Request: req, Response: resp, Err: err})
}

// afterTool notifies the AfterTool hook, if configured.
func (h Hooks) afterTool(ctx context.Context, ev *ToolEvent) {
	if h.AfterTool == nil {
		return
	}
	h.AfterTool(ctx, ev)
}

// storageError notifies the OnStorageError hook, if configured.
func (h Hooks) storageError(ctx context.Context, op string, err error) {
	if err == nil || h.OnStorageError == nil {
		return
	}
	h.OnStorageError(ctx, op, err)
}
