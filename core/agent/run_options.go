package agent

import (
	"slices"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// RunOptions configures one agent run. It is the single public configuration
// type; execution internals stay unexported.
type RunOptions struct {
	// Session identifies the conversation. It is required when Store is set.
	Session *session.Session
	// Store persists the conversation. It is optional.
	Store session.Store
	// Policy controls model and tool calls. The agent policy is used when nil.
	Policy Policy
	// Approver resolves Ask decisions. Without one, Ask is treated as Deny.
	Approver Approver
	// Sink observes run events.
	Sink Sink
	// Budget bounds the run.
	Budget Budget
	// Retry configures transient model retries.
	Retry RetryPolicy
	// ToolTimeout bounds each tool execution. Zero uses DefaultToolTimeout.
	// A negative value disables the timeout.
	ToolTimeout time.Duration

	// Model overrides the agent model.
	Model llm.Model
	// Tools adds or replaces agent tools by name.
	Tools []tool.Tool
	// Thinking overrides reasoning configuration.
	Thinking *llm.ThinkingConfig
	// ResponseFormat constrains structured output.
	ResponseFormat *llm.ResponseFormat
	// Temperature overrides sampling temperature.
	Temperature *float64
	// MaxTokens overrides the output token limit.
	MaxTokens *int
	// TopP overrides nucleus sampling.
	TopP *float64
	// Media attaches parts to the trailing user message.
	Media []llm.Part
	// ToolChoice constrains tool selection.
	ToolChoice llm.ToolChoice

	// optionErr records a failure from an Option so newRunner can return it.
	optionErr error
}

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
	o.Thinking = o.Thinking.Clone()
	o.ResponseFormat = o.ResponseFormat.Clone()
	o.Temperature = cloneFloat(o.Temperature)
	o.MaxTokens = cloneInt(o.MaxTokens)
	o.TopP = cloneFloat(o.TopP)
	o.Media = cloneParts(o.Media)
	return o
}
