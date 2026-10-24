package agent

import (
	"cmp"
	"fmt"
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
	// Policy is the agent's default policy. A Runner policy may override it.
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

	tools := make([]tool.Tool, 0, len(cfg.Tools))
	seen := make(map[string]struct{}, len(cfg.Tools))
	for _, candidate := range cfg.Tools {
		if tool.IsNilTool(candidate) {
			return nil, ErrToolNil
		}
		name := candidate.Spec().Name
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateTool, name)
		}
		seen[name] = struct{}{}
		tools = append(tools, candidate)
	}
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

// Tools returns a copy of the tool list.
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

// mergeTools builds a name-indexed tool registry from the agent and per-run
// tools. Later entries with the same name replace earlier ones.
type toolRegistry struct {
	ordered []tool.Tool
	byName  map[string]tool.Tool
}

func newToolRegistry(agentTools, extra []tool.Tool) *toolRegistry {
	merged := make(map[string]tool.Tool, len(agentTools)+len(extra))
	for _, candidate := range agentTools {
		if !tool.IsNilTool(candidate) {
			merged[candidate.Spec().Name] = candidate
		}
	}
	for _, candidate := range extra {
		if !tool.IsNilTool(candidate) {
			merged[candidate.Spec().Name] = candidate
		}
	}
	ordered := make([]tool.Tool, 0, len(merged))
	for _, candidate := range merged {
		ordered = append(ordered, candidate)
	}
	slices.SortFunc(ordered, func(a, b tool.Tool) int {
		return cmp.Compare(a.Spec().Name, b.Spec().Name)
	})
	return &toolRegistry{ordered: ordered, byName: merged}
}

func (r *toolRegistry) definitions() []llm.ToolDefinition {
	if r == nil {
		return nil
	}
	return tool.Definitions(r.ordered)
}
