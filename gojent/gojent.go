// Package gojent provides the root container for registering, managing, and executing agents and workflows.
package gojent

import (
	"fmt"
	"maps"
	"sync"

	"github.com/ijaidev/gojent/core/agent"
)

// Gojent is the central container managing agents, workflows, and integrations,
type Gojent struct {
	mu     sync.RWMutex
	agents map[string]*agent.Agent
}

// Option configures a Gojent instance.
type Option func(*Gojent)

// WithAgent registers an agent with the Gojent container.
func WithAgent(a *agent.Agent) Option {
	return func(g *Gojent) {
		if a != nil {
			g.agents[a.ID()] = a
		}
	}
}

// New creates and initializes a new Gojent root container.
//
// Example:
//
//	g := gojent.New(
//	    gojent.WithAgent(chefAgent),
//	)
//	chef, err := g.GetAgent("chef-agent")
func New(opts ...Option) *Gojent {
	g := &Gojent{
		agents: make(map[string]*agent.Agent),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// RegisterAgent registers an agent at runtime.
func (g *Gojent) RegisterAgent(a *agent.Agent) error {
	if a == nil {
		return fmt.Errorf("gojent: cannot register nil agent")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agents[a.ID()] = a
	return nil
}

// GetAgent retrieves a registered agent by ID.
func (g *Gojent) GetAgent(id string) (*agent.Agent, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	a, ok := g.agents[id]
	if !ok {
		return nil, fmt.Errorf("gojent: agent with id '%s' not found", id)
	}
	return a, nil
}

// ListAgents returns a copy of all registered agents mapped by ID.
func (g *Gojent) ListAgents() map[string]*agent.Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make(map[string]*agent.Agent, len(g.agents))
	maps.Copy(result, g.agents)
	return result
}
