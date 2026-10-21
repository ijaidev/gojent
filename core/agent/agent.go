// Package agent provides autonomous agent abstractions, configuration, execution loops,
// and lifecycle hooks for observing and customizing runs.
package agent

import (
	"sync"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

const defaultMaxSteps = 5

// Config defines the initialization options for an Agent.
type Config struct {
	// ID is the unique identifier for the agent (e.g. "chef-agent").
	ID string
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
	toolsMap := make(map[string]tool.Tool, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if !tool.IsNilTool(t) {
			toolsMap[t.ID()] = t
		}
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	return &Agent{
		id:           cfg.ID,
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

// MaxSteps returns the default maximum number of ReAct loop iterations for this agent.
func (a *Agent) MaxSteps() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maxSteps
}

// Thinking returns the default thinking configuration for this agent, or nil.
func (a *Agent) Thinking() *llm.ThinkingConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinking.Clone()
}

// Hooks returns the lifecycle hooks configured on this agent.
func (a *Agent) Hooks() Hooks {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hooks
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

// Storage returns the configured storage backend for this agent, or nil.
func (a *Agent) Storage() storage.Storage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.storage
}
