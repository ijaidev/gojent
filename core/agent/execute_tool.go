package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// DefaultToolTimeout bounds a single tool execution when
// RunOptions.ToolTimeout is zero.
const DefaultToolTimeout = 2 * time.Minute

func (r *runner) runTools(ctx context.Context, calls []llm.ToolCall, step int) ([]llm.Message, bool, error) {
	results := make([]llm.Message, 0, len(calls))
	budgetExceeded := false

	for index, raw := range calls {
		call := tool.Call{
			ID:             raw.ID,
			Index:          index,
			Name:           raw.Name,
			Arguments:      json.RawMessage(raw.Arguments),
			IdempotencyKey: raw.ID,
		}
		spec := tool.Spec{}
		impl, found := r.tools.byName[call.Name]
		if found {
			spec = impl.Spec()
		}
		safeEmit(r.opts.Sink, ToolStart{Step: step, Call: call, Spec: spec})

		if r.opts.Budget.MaxToolCalls > 0 && r.toolCalls >= r.opts.Budget.MaxToolCalls {
			budgetExceeded = true
			message := toolErrorMessage(call, fmt.Errorf("%w: tool call budget exceeded", ErrBudgetExceeded))
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrBudgetExceeded})
			results = append(results, message)
			continue
		}

		decision, policyErr := r.beforeTool(ctx, step, call, spec)
		if policyErr != nil {
			return nil, false, policyErr
		}
		if decision == Terminate {
			return nil, false, ErrTerminated
		}
		if decision != Allow {
			// Deny, an unresolved Ask, and unknown decisions all fail closed.
			message := toolErrorMessage(call, ErrToolDenied)
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrToolDenied})
			results = append(results, message)
			continue
		}

		if !found {
			err := fmt.Errorf("agent: tool %q not found", call.Name)
			message := toolErrorMessage(call, err)
			if afterErr := r.afterTool(ctx, step, call, nil, err); afterErr != nil {
				return nil, false, afterErr
			}
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: err})
			results = append(results, message)
			continue
		}

		r.toolCalls++
		result, execErr := r.executeTool(ctx, impl, call)
		if afterErr := r.afterTool(ctx, step, call, &result, execErr); afterErr != nil {
			return nil, false, afterErr
		}
		if execErr != nil {
			message := toolErrorMessage(call, execErr)
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: execErr})
			results = append(results, message)
			continue
		}

		message, encodeErr := resultMessage(call, result)
		if encodeErr != nil {
			message = toolErrorMessage(call, encodeErr)
		}
		safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: encodeErr})
		results = append(results, message)
	}
	return results, budgetExceeded, nil
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
