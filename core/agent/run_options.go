package agent

import (
	"slices"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// RunOptions configures one agent run. Unset values inherit the agent's
// configuration; remaining zero values fall back to package defaults. It is
// the single public per-run configuration type.
type RunOptions struct {
	// Session identifies the conversation. It is required when Store is set.
	Session *session.Session
	// Store persists the conversation. It is optional; the agent Store is used
	// when nil.
	Store session.Store
	// Policy controls model and tool calls. The agent policy is used when nil.
	Policy Policy
	// Approver resolves Ask decisions. The agent default Approver is used when
	// nil. Without an approver, Ask is treated as Deny.
	Approver Approver
	// Sink observes run events.
	Sink Sink
	// Budget bounds the run. Non-zero fields override the agent budget.
	Budget Budget
	// Retry configures transient model retries. Non-zero fields override the
	// agent retry policy.
	Retry RetryPolicy
	// ToolTimeout bounds each tool execution. Zero inherits the agent default,
	// then DefaultToolTimeout. A negative value disables the timeout.
	ToolTimeout time.Duration

	// Model overrides the agent model.
	Model llm.Model
	// Tools adds or replaces agent tools by name.
	Tools []tool.Tool
	// Reasoning overrides reasoning configuration. The agent default is used
	// when nil.
	Reasoning *llm.ReasoningConfig
	// ResponseFormat constrains structured output. The agent default is used
	// when nil.
	ResponseFormat *llm.ResponseFormat
	// Temperature overrides sampling temperature. The agent default is used
	// when nil.
	Temperature *float64
	// MaxTokens overrides the output token limit. The agent default is used
	// when nil.
	MaxTokens *int
	// TopP overrides nucleus sampling. The agent default is used when nil.
	TopP *float64
	// Media attaches parts to the trailing user message.
	Media []llm.Part
	// ToolChoice constrains tool selection. The agent default is used when
	// empty.
	ToolChoice llm.ToolChoice

	// optionErr records a failure from an Option so newRunner can return it.
	optionErr error
}

// Option customizes one Agent run.
type Option func(*RunOptions)

func resolveOptions(opts []Option) RunOptions {
	var cfg RunOptions
	for _, option := range opts {
		if option != nil {
			option(&cfg)
		}
	}
	return cfg
}

// snapshot copies the mutable parts of the configuration so a run is isolated
// from changes made by the caller after it starts.
func (o RunOptions) snapshot() RunOptions {
	o.Session = cloneSession(o.Session)
	o.Tools = slices.Clone(o.Tools)
	o.Reasoning = o.Reasoning.Clone()
	o.ResponseFormat = o.ResponseFormat.Clone()
	o.Temperature = cloneFloat(o.Temperature)
	o.MaxTokens = cloneInt(o.MaxTokens)
	o.TopP = cloneFloat(o.TopP)
	o.Media = llm.CloneParts(o.Media)
	return o
}
