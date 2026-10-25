package agent

import "github.com/ijaidev/gojent/core/llm"

// ApplyAgentOptions fills unset run options from the agent's configuration.
// Explicit run values win; zero values inherit the agent-level value. Budget
// and RetryPolicy merge field by field, so a run can change one limit without
// dropping the others.
//
// The returned options never alias the agent's pointer-valued sampling
// options: inherited values are cloned before they are applied. Interface
// values such as Model, Policy, Approver, and Store are shared. A nil agent
// returns opts unchanged.
func (a *Agent) ApplyAgentOptions(opts RunOptions) RunOptions {
	if a == nil {
		return opts
	}

	if opts.Model == nil {
		opts.Model = a.model
	}
	if opts.Policy == nil {
		opts.Policy = a.policy
	}
	if opts.Approver == nil {
		opts.Approver = a.approver
	}
	if opts.Store == nil {
		opts.Store = a.store
	}
	opts.Budget = a.budget.withOverride(opts.Budget)
	opts.Retry = a.retry.withOverride(opts.Retry)
	if opts.ToolTimeout == 0 {
		opts.ToolTimeout = a.toolTimeout
	}
	if opts.Reasoning == nil {
		opts.Reasoning = a.reasoning.Clone()
	}
	if opts.ResponseFormat == nil {
		opts.ResponseFormat = a.responseFormat.Clone()
	}
	if opts.Temperature == nil {
		opts.Temperature = cloneFloat(a.temperature)
	}
	if opts.MaxTokens == nil {
		opts.MaxTokens = cloneInt(a.maxTokens)
	}
	if opts.TopP == nil {
		opts.TopP = cloneFloat(a.topP)
	}
	if opts.ToolChoice == (llm.ToolChoice{}) {
		opts.ToolChoice = a.toolChoice
	}
	return opts
}

// withOverride merges non-zero run fields over the agent defaults.
func (b Budget) withOverride(run Budget) Budget {
	if run.MaxSteps != 0 {
		b.MaxSteps = run.MaxSteps
	}
	if run.MaxToolCalls != 0 {
		b.MaxToolCalls = run.MaxToolCalls
	}
	if run.MaxTotalTokens != 0 {
		b.MaxTotalTokens = run.MaxTotalTokens
	}
	if run.MaxDuration != 0 {
		b.MaxDuration = run.MaxDuration
	}
	return b
}

// withOverride merges non-zero run fields over the agent defaults.
func (p RetryPolicy) withOverride(run RetryPolicy) RetryPolicy {
	if run.MaxAttempts != 0 {
		p.MaxAttempts = run.MaxAttempts
	}
	if run.InitialDelay != 0 {
		p.InitialDelay = run.InitialDelay
	}
	if run.MaxDelay != 0 {
		p.MaxDelay = run.MaxDelay
	}
	return p
}
