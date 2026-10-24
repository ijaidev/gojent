// Package agent provides the agent runtime: one streaming-first execution
// loop, typed tools, provider-neutral models, optional sessions, policy and
// event seams.
package agent

import (
	"cmp"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// Agent is reusable behavior: instructions, a model, tools, and an optional
// default policy. It does not own storage or execution state.
type Agent struct {
	name         string
	description  string
	instructions string
	model        llm.Model
	tools        []tool.Tool
	toolByName   map[string]tool.Tool
	policy       Policy
}

// Config configures an Agent.
type Config struct {
	// Name uniquely identifies the agent. Required.
	Name string
	// Description explains the agent's purpose, especially when exposed as a
	// tool for subagents.
	Description string
	// Instructions is the system prompt.
	Instructions string
	// Model powers the agent. Required.
	Model llm.Model
	// Tools are the capabilities available to the model.
	Tools []tool.Tool
	// Policy is the agent's default policy. A RunOptions policy may override it.
	Policy Policy
}

// New creates an immutable Agent.
func New(cfg Config) (*Agent, error) {
	if cfg.Name == "" {
		return nil, ErrNameRequired
	}
	if cfg.Model == nil {
		return nil, ErrModelRequired
	}

	if err := validateTools(cfg.Tools); err != nil {
		return nil, err
	}
	tools := slices.Clone(cfg.Tools)
	slices.SortFunc(tools, func(a, b tool.Tool) int {
		return cmp.Compare(a.Spec().Name, b.Spec().Name)
	})

	byName := make(map[string]tool.Tool, len(tools))
	for _, candidate := range tools {
		byName[candidate.Spec().Name] = candidate
	}

	return &Agent{
		name:         cfg.Name,
		description:  cfg.Description,
		instructions: cfg.Instructions,
		model:        cfg.Model,
		tools:        tools,
		toolByName:   byName,
		policy:       cfg.Policy,
	}, nil
}

// Name returns the agent name.
func (a *Agent) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

// Description returns the agent description.
func (a *Agent) Description() string {
	if a == nil {
		return ""
	}
	return a.description
}

// Instructions returns the system instructions.
func (a *Agent) Instructions() string {
	if a == nil {
		return ""
	}
	return a.instructions
}

// Model returns the agent model.
func (a *Agent) Model() llm.Model {
	if a == nil {
		return nil
	}
	return a.model
}

// Tools returns a copy of the tool list, sorted by name.
func (a *Agent) Tools() []tool.Tool {
	if a == nil {
		return nil
	}
	return slices.Clone(a.tools)
}

// Policy returns the agent's default policy, or nil.
func (a *Agent) Policy() Policy {
	if a == nil {
		return nil
	}
	return a.policy
}

// Tool looks up a tool by name.
func (a *Agent) Tool(name string) (tool.Tool, bool) {
	if a == nil {
		return nil, false
	}
	candidate, ok := a.toolByName[name]
	return candidate, ok
}
