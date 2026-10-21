// Package provider defines the model-provider seam used by codjent.
//
// A Registry resolves a provider/model pair to an llm.LLM. Providers are
// registered at startup; the app stores only their string identifiers.
package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrUnknownProvider is returned when no registered provider matches an ID.
	ErrUnknownProvider = errors.New("provider: unknown provider")
	// ErrUnknownModel is returned when a provider does not expose a model ID.
	ErrUnknownModel = errors.New("provider: unknown model")
)

// ModelInfo describes one selectable model.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Info describes a provider and the models it exposes.
type Info struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	DefaultModel string      `json:"default_model"`
	Models       []ModelInfo `json:"models"`
}

// Provider is one model backend. Implementations own provider-specific
// configuration and client construction.
type Provider interface {
	Info() Info
	NewModel(ctx context.Context, modelID string) (llm.LLM, error)
}

// ModelSource is the read-only seam the app uses: list choices and construct a
// model for one run.
type ModelSource interface {
	List() []Info
	NewModel(ctx context.Context, providerID, modelID string) (llm.LLM, error)
}

// Registry is an immutable set of providers in registration order.
type Registry struct {
	providers map[string]Provider
	order     []string
}

// NewRegistry validates and registers providers.
func NewRegistry(providers ...Provider) (*Registry, error) {
	if len(providers) == 0 {
		return nil, errors.New("provider: at least one provider is required")
	}

	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if p == nil {
			return nil, errors.New("provider: nil provider")
		}
		info := p.Info()
		if info.ID == "" {
			return nil, errors.New("provider: provider ID is required")
		}
		if _, exists := r.providers[info.ID]; exists {
			return nil, fmt.Errorf("provider: duplicate provider %q", info.ID)
		}
		r.providers[info.ID] = p
		r.order = append(r.order, info.ID)
	}
	return r, nil
}

// List returns provider metadata in registration order.
func (r *Registry) List() []Info {
	infos := make([]Info, 0, len(r.order))
	for _, id := range r.order {
		info := r.providers[id].Info()
		info.Models = slices.Clone(info.Models)
		infos = append(infos, info)
	}
	return infos
}

// NewModel resolves and constructs a model. Empty IDs select the first
// provider and its default model.
func (r *Registry) NewModel(ctx context.Context, providerID, modelID string) (llm.LLM, error) {
	if providerID == "" {
		if len(r.order) == 0 {
			return nil, ErrUnknownProvider
		}
		providerID = r.order[0]
	}

	p, ok := r.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, providerID)
	}

	info := p.Info()
	if modelID == "" {
		modelID = info.DefaultModel
	}
	if !hasModel(info.Models, modelID) {
		return nil, fmt.Errorf("%w: %s/%s", ErrUnknownModel, providerID, modelID)
	}

	model, err := p.NewModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", providerID, err)
	}
	return model, nil
}

func hasModel(models []ModelInfo, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}
