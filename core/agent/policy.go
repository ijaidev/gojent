package agent

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// Decision is a policy's verdict for a tool call.
type Decision int

const (
	// Allow executes the tool.
	Allow Decision = iota
	// Deny skips the tool and reports the denial to the model.
	Deny
	// Ask requires an approver. Without one, the call is denied.
	Ask
	// Terminate aborts the run.
	Terminate
)

// String renders a decision.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case Ask:
		return "ask"
	case Terminate:
		return "terminate"
	default:
		return "unknown"
	}
}

// ModelRequest is passed to Policy.BeforeModel.
type ModelRequest struct {
	Step    int
	Request *llm.Request
}

// ModelResult is passed to Policy.AfterModel.
type ModelResult struct {
	Step     int
	Request  *llm.Request
	Response *llm.Response
	Err      error
}

// ToolRequest is passed to Policy.BeforeTool.
type ToolRequest struct {
	Step int
	Call tool.Call
	Spec tool.Spec
}

// ToolResult is passed to Policy.AfterTool.
type ToolResult struct {
	Step   int
	Call   tool.Call
	Result *tool.Result
	Err    error
}

// Policy controls a run. Sinks observe; policies decide.
//
// BeforeModel and AfterModel may mutate the request/response. Returning an
// error aborts the run. BeforeTool returns a Decision; Deny tells the model,
// Ask delegates to an Approver, and Terminate aborts.
type Policy interface {
	BeforeModel(ctx context.Context, req *ModelRequest) error
	AfterModel(ctx context.Context, result *ModelResult) error
	BeforeTool(ctx context.Context, req *ToolRequest) (Decision, error)
	AfterTool(ctx context.Context, result *ToolResult) error
}

// Hooks is a function-field Policy. Every field is optional.
type Hooks struct {
	OnBeforeModel func(ctx context.Context, req *ModelRequest) error
	OnAfterModel  func(ctx context.Context, result *ModelResult)
	OnBeforeTool  func(ctx context.Context, req *ToolRequest) (Decision, error)
	OnAfterTool   func(ctx context.Context, result *ToolResult)
}

// BeforeModel implements Policy.
func (h Hooks) BeforeModel(ctx context.Context, req *ModelRequest) error {
	if h.OnBeforeModel == nil {
		return nil
	}
	return h.OnBeforeModel(ctx, req)
}

// AfterModel implements Policy.
func (h Hooks) AfterModel(ctx context.Context, result *ModelResult) error {
	if h.OnAfterModel != nil {
		h.OnAfterModel(ctx, result)
	}
	return nil
}

// BeforeTool implements Policy.
func (h Hooks) BeforeTool(ctx context.Context, req *ToolRequest) (Decision, error) {
	if h.OnBeforeTool == nil {
		return Allow, nil
	}
	return h.OnBeforeTool(ctx, req)
}

// AfterTool implements Policy.
func (h Hooks) AfterTool(ctx context.Context, result *ToolResult) error {
	if h.OnAfterTool != nil {
		h.OnAfterTool(ctx, result)
	}
	return nil
}

// Approver resolves Ask decisions. An approval request contains the same call
// metadata a policy sees.
type Approver interface {
	Approve(ctx context.Context, req ToolRequest) (bool, error)
}

// ApproverFunc adapts a function to Approver.
type ApproverFunc func(ctx context.Context, req ToolRequest) (bool, error)

// Approve implements Approver.
func (f ApproverFunc) Approve(ctx context.Context, req ToolRequest) (bool, error) {
	return f(ctx, req)
}

// PolicyChain composes policies in order. Before hooks run in order and
// After hooks run in reverse order.
type PolicyChain []Policy

// BeforeModel implements Policy.
func (c PolicyChain) BeforeModel(ctx context.Context, req *ModelRequest) error {
	for _, policy := range c {
		if policy == nil {
			continue
		}
		if err := policy.BeforeModel(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// AfterModel implements Policy. After hooks run in reverse order.
func (c PolicyChain) AfterModel(ctx context.Context, result *ModelResult) error {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		if err := c[i].AfterModel(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

// BeforeTool implements Policy. The most restrictive decision wins, and
// Terminate wins over Ask, which wins over Deny.
func (c PolicyChain) BeforeTool(ctx context.Context, req *ToolRequest) (Decision, error) {
	decision := Allow
	for _, policy := range c {
		if policy == nil {
			continue
		}
		next, err := policy.BeforeTool(ctx, req)
		if err != nil {
			return Deny, err
		}
		if next > decision {
			decision = next
		}
	}
	return decision, nil
}

// AfterTool implements Policy. After hooks run in reverse order.
func (c PolicyChain) AfterTool(ctx context.Context, result *ToolResult) error {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		if err := c[i].AfterTool(ctx, result); err != nil {
			return err
		}
	}
	return nil
}
