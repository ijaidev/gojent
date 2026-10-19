// Package agent provides autonomous agent abstractions, configuration, execution loops,
// and lifecycle hooks for observing and customizing runs.
package agent

import (
	"errors"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

var (
	// ErrModelRequired is returned when an Agent is created without an LLM model.
	ErrModelRequired = errors.New("agent: model is required")
	// ErrIDRequired is returned when an Agent is created without an ID.
	ErrIDRequired = errors.New("agent: id is required")
	// ErrToolNil is returned when attempting to add a nil tool to an Agent.
	ErrToolNil = errors.New("agent: cannot add nil tool")
)

const defaultMaxSteps = 5

// Config defines the initialization options for an Agent.
type Config struct {
	// ID is the unique identifier for the agent (e.g. "chef-agent").
	ID string
	// Name is a human-readable display name for the agent.
	Name string
	// Description explains the agent's purpose and specialization.
	Description string
	// Instructions are the system instructions/prompt guiding agent behavior.
	Instructions string
	// Model is the swappable LLM instance that powers the agent.
	Model llm.LLM
	// Storage is the optional swappable storage backend for conversation threads, messages, and state.
	Storage storage.Storage
	// Tools contains the list of tools available to the agent.
	Tools []tool.Tool
	// MaxSteps sets the default maximum number of ReAct loop iterations (defaults to 5).
	MaxSteps int
	// Thinking configures default thinking/reasoning parameters for the agent.
	Thinking *llm.ThinkingConfig
	// Hooks defines optional lifecycle callbacks for observing and customizing runs.
	Hooks Hooks
}

// Agent represents an autonomous AI agent that leverages a Large Language Model.
// It encapsulates instructions, swappable model dependencies, and execution methods.
type Agent struct {
	mu           sync.RWMutex
	id           string
	name         string
	description  string
	instructions string
	model        llm.LLM
	storage      storage.Storage
	tools        map[string]tool.Tool
	hooks        Hooks
	maxSteps     int
	thinking     *llm.ThinkingConfig
}

// New creates and initializes a new Agent with the given configuration.
func New(cfg Config) (*Agent, error) {
	if cfg.ID == "" {
		return nil, ErrIDRequired
	}
	if cfg.Model == nil {
		return nil, ErrModelRequired
	}
	name := cfg.Name
	if name == "" {
		name = cfg.ID
	}

	toolsMap := make(map[string]tool.Tool, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if t != nil {
			toolsMap[t.ID()] = t
		}
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	return &Agent{
		id:           cfg.ID,
		name:         name,
		description:  cfg.Description,
		instructions: cfg.Instructions,
		model:        cfg.Model,
		storage:      cfg.Storage,
		tools:        toolsMap,
		hooks:        cfg.Hooks,
		maxSteps:     maxSteps,
		thinking:     cfg.Thinking.Clone(),
	}, nil
}

// ID returns the unique ID of the agent.
func (a *Agent) ID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.id
}

// Name returns the name of the agent.
func (a *Agent) Name() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.name
}

// Description returns the description of the agent.
func (a *Agent) Description() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.description
}

// Instructions returns the agent's system instructions.
func (a *Agent) Instructions() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.instructions
}

// Model returns the current swappable LLM used by this agent.
func (a *Agent) Model() llm.LLM {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// Tools returns a snapshot slice of all registered tools on this agent.
func (a *Agent) Tools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make([]tool.Tool, 0, len(a.tools))
	for _, t := range a.tools {
		res = append(res, t)
	}
	return res
}

// GetTool retrieves a registered tool by its unique identifier.
func (a *Agent) GetTool(id string) (tool.Tool, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	t, ok := a.tools[id]
	return t, ok
}

// AddTool registers a new tool on the agent.
func (a *Agent) AddTool(t tool.Tool) error {
	if t == nil {
		return ErrToolNil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools[t.ID()] = t
	return nil
}

// RemoveTool unregisters a tool from the agent by ID.
func (a *Agent) RemoveTool(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tools, id)
}

// MaxSteps returns the default maximum number of ReAct loop iterations for this agent.
func (a *Agent) MaxSteps() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maxSteps
}

// SetMaxSteps updates the default maximum number of ReAct loop iterations for this agent.
func (a *Agent) SetMaxSteps(n int) {
	if n <= 0 {
		n = defaultMaxSteps
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxSteps = n
}

// Thinking returns the default thinking configuration for this agent, or nil.
func (a *Agent) Thinking() *llm.ThinkingConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinking.Clone()
}

// SetThinking updates the default thinking configuration for this agent at runtime.
func (a *Agent) SetThinking(cfg *llm.ThinkingConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinking = cfg.Clone()
}

// Hooks returns the lifecycle hooks configured on this agent.
func (a *Agent) Hooks() Hooks {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hooks
}

// SetHooks replaces the lifecycle hooks configured on this agent at runtime.
// Per-call hooks supplied via WithHooks override matching callbacks per field.
func (a *Agent) SetHooks(h Hooks) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hooks = h
}

// SetModel swaps the LLM implementation for this agent at runtime.
func (a *Agent) SetModel(m llm.LLM) error {
	if m == nil {
		return ErrModelRequired
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = m
	return nil
}

// SetInstructions updates the system instructions for this agent at runtime.
func (a *Agent) SetInstructions(instructions string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.instructions = instructions
}

// Storage returns the configured storage backend for this agent, or nil.
func (a *Agent) Storage() storage.Storage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.storage
}

// SetStorage sets or swaps the storage backend for this agent at runtime.
func (a *Agent) SetStorage(s storage.Storage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.storage = s
}
