package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// DefaultToolTimeout bounds a single tool execution when
// RunOptions.ToolTimeout is zero.
const DefaultToolTimeout = 2 * time.Minute

// runTools executes locally-runnable calls and groups every result into one
// user message, in call order. Server calls are recorded in the assistant
// message and never executed.
func (r *runner) runTools(ctx context.Context, assistant llm.Message, step int) (llm.Message, bool, error) {
	results := make([]llm.ToolResult, 0, len(assistant.ToolCalls()))
	budgetExceeded := false

	for index, part := range assistant.Content {
		callPart, ok := part.(*llm.ToolCallPart)
		if !ok {
			continue
		}
		raw := callPart.ToolCall
		if raw.EffectiveKind() == llm.ToolKindServer {
			continue
		}
		call := tool.Call{
			ID:             raw.CallID,
			Index:          index,
			Name:           raw.Name,
			Kind:           raw.EffectiveKind(),
			Arguments:      append([]byte(nil), raw.Input...),
			IdempotencyKey: raw.CallID,
		}
		spec := tool.Spec{}
		impl, found := r.tools.byName[call.Name]
		if found {
			spec = impl.Spec()
		}
		safeEmit(r.opts.Sink, ToolStart{Step: step, Call: call, Spec: spec})

		outcome := r.runToolCall(ctx, impl, found, call, spec, step)
		var message *llm.Message
		if outcome.hasResult {
			observed := resultMessage(outcome.result)
			message = &observed
			results = append(results, outcome.result)
		}
		safeEmit(r.opts.Sink, ToolEnd{
			Step:    step,
			Call:    call,
			Result:  outcome.raw,
			Message: message,
			Err:     outcome.err,
		})
		if outcome.fatal != nil {
			return llm.Message{}, false, outcome.fatal
		}
		budgetExceeded = budgetExceeded || outcome.budgetExceeded
	}

	if len(results) == 0 {
		return llm.Message{}, budgetExceeded, nil
	}
	return llm.ToolResultsMessage(results...), budgetExceeded, nil
}

// toolCallOutcome is the result of one tool call. fatal aborts the run before
// a result can be recorded. raw is the tool package result for policy hooks.
type toolCallOutcome struct {
	result         llm.ToolResult
	hasResult      bool
	raw            *tool.Result
	err            error
	fatal          error
	budgetExceeded bool
}

// runToolCall applies the tool budget and policy, executes the tool, and
// encodes its result. Fatal policy failures still return an outcome so the
// caller can close the ToolStart/ToolEnd pair.
func (r *runner) runToolCall(ctx context.Context, impl tool.Tool, found bool, call tool.Call, spec tool.Spec, step int) toolCallOutcome {
	if r.opts.Budget.MaxToolCalls > 0 && r.toolCalls >= r.opts.Budget.MaxToolCalls {
		err := fmt.Errorf("%w: tool call budget exceeded", ErrBudgetExceeded)
		return toolCallOutcome{
			result:         toolErrorResult(call, llm.ToolStatusError, err),
			hasResult:      true,
			err:            err,
			budgetExceeded: true,
		}
	}

	decision, policyErr := r.beforeTool(ctx, step, call, spec)
	if policyErr != nil {
		return toolCallOutcome{err: policyErr, fatal: policyErr}
	}
	if decision == Terminate {
		return toolCallOutcome{err: ErrTerminated, fatal: ErrTerminated}
	}
	if decision != Allow {
		// Deny, an unresolved Ask, and unknown decisions all fail closed.
		return toolCallOutcome{
			result:    toolErrorResult(call, llm.ToolStatusDenied, ErrToolDenied),
			hasResult: true,
			err:       ErrToolDenied,
		}
	}

	if !found {
		err := fmt.Errorf("agent: tool %q not found", call.Name)
		if afterErr := r.afterTool(ctx, step, call, nil, err); afterErr != nil {
			return toolCallOutcome{err: afterErr, fatal: afterErr}
		}
		return toolCallOutcome{
			result:    toolErrorResult(call, llm.ToolStatusError, err),
			hasResult: true,
			err:       err,
		}
	}

	r.toolCalls++
	result, execErr := r.executeTool(ctx, impl, call)
	if afterErr := r.afterTool(ctx, step, call, &result, execErr); afterErr != nil {
		return toolCallOutcome{raw: &result, err: afterErr, fatal: afterErr}
	}
	if execErr != nil {
		return toolCallOutcome{
			result:    toolErrorResult(call, llm.ToolStatusError, execErr),
			hasResult: true,
			raw:       &result,
			err:       execErr,
		}
	}

	canonical, encodeErr := canonicalResult(call, result, llm.ToolStatusCompleted)
	if encodeErr != nil {
		return toolCallOutcome{
			result:    toolErrorResult(call, llm.ToolStatusError, encodeErr),
			hasResult: true,
			raw:       &result,
			err:       encodeErr,
		}
	}
	return toolCallOutcome{result: canonical, hasResult: true, raw: &result}
}

func (r *runner) beforeTool(ctx context.Context, step int, call tool.Call, spec tool.Spec) (Decision, error) {
	if r.opts.Policy == nil {
		return Allow, nil
	}
	var decision Decision
	err := callPolicy(func() error {
		var policyErr error
		decision, policyErr = r.opts.Policy.BeforeTool(ctx, &ToolRequest{Step: step, Call: call, Spec: spec})
		return policyErr
	})
	if err != nil {
		return Deny, err
	}
	if decision == Ask {
		if r.opts.Approver == nil {
			return Deny, nil
		}
		approved, approveErr := callApprove(func() (bool, error) {
			return r.opts.Approver.Approve(ctx, ToolRequest{Step: step, Call: call, Spec: spec})
		})
		if approveErr != nil {
			return Deny, approveErr
		}
		if !approved {
			return Deny, nil
		}
		return Allow, nil
	}
	return decision, nil
}

func (r *runner) afterTool(ctx context.Context, step int, call tool.Call, result *tool.Result, err error) error {
	if r.opts.Policy == nil {
		return nil
	}
	return callPolicy(func() error {
		return r.opts.Policy.AfterTool(ctx, &ToolResult{Step: step, Call: call, Result: result, Err: err})
	})
}

func (r *runner) executeTool(ctx context.Context, impl tool.Tool, call tool.Call) (tool.Result, error) {
	timeout := r.opts.ToolTimeout
	if timeout == 0 {
		timeout = DefaultToolTimeout
	}
	if timeout <= 0 {
		return callTool(ctx, impl, call)
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		result tool.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- outcome{err: &PanicError{Value: recovered, Stack: debug.Stack()}}
			}
		}()
		result, err := impl.Execute(toolCtx, call)
		done <- outcome{result: result, err: err}
	}()

	select {
	case out := <-done:
		return out.result, out.err
	case <-toolCtx.Done():
		return tool.Result{}, fmt.Errorf("agent: tool %q: %w", call.Name, toolCtx.Err())
	}
}
