// Package provider defines the model-provider seam used by codjent.
//
// A ModelSource resolves a provider/model pair to an llm.Model. The Registry
// implementation is a static set registered at startup; codjent's catalog
// implementation builds the set from persisted configuration.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrUnknownProvider is returned when no registered provider matches an ID.
	ErrUnknownProvider = errors.New("provider: unknown provider")
	// ErrUnknownModel is returned when a provider does not expose a model ID.
	ErrUnknownModel = errors.New("provider: unknown model")
	// ErrInvalidConfig is returned when a provider configuration fails validation.
	ErrInvalidConfig = errors.New("provider: invalid configuration")
	// ErrConfigNotFound is returned when a provider configuration does not exist.
	ErrConfigNotFound = errors.New("provider: provider configuration not found")
	// ErrConfigInUse is returned when threads still reference a configuration.
	ErrConfigInUse = errors.New("provider: provider configuration is used by threads")
)

// Format identifies the wire protocol a provider adapter speaks. It is not the
// vendor: any OpenAI-compatible endpoint uses one of the OpenAI formats.
type Format string

const (
	// FormatOpenAIResponses is the OpenAI Responses API.
	FormatOpenAIResponses Format = "openai-responses"
	// FormatOpenAICompletion is the OpenAI Chat Completions API.
	FormatOpenAICompletion Format = "openai-completion"
	// FormatAnthropic is the Anthropic Messages API.
	FormatAnthropic Format = "anthropic"
	// FormatGoogle is the Google Gen AI API.
	FormatGoogle Format = "google"
)

// Formats returns every supported format in display order.
func Formats() []Format {
	return []Format{
		FormatOpenAIResponses,
		FormatOpenAICompletion,
		FormatAnthropic,
		FormatGoogle,
	}
}

// Valid reports whether the format has a known adapter.
func (f Format) Valid() bool {
	return slices.Contains(Formats(), f)
}

const maxNameLength = 80

// Config is one persisted provider configuration as exposed over the API. The
// API key is never serialized: APIKeySet reports whether one is stored.
type Config struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Format       Format      `json:"format"`
	BaseURL      string      `json:"base_url"`
	DefaultModel string      `json:"default_model"`
	Models       []ModelInfo `json:"models"`
	// APIKey is the stored secret. It is deliberately excluded from JSON and
	// must never be returned to a client.
	APIKey    string    `json:"-"`
	APIKeySet bool      `json:"api_key_set"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConfigInput is the create or update payload for a provider configuration.
//
// On update an empty APIKey keeps the stored secret. ClearAPIKey removes it.
type ConfigInput struct {
	Name         string      `json:"name"`
	Format       Format      `json:"format"`
	BaseURL      string      `json:"base_url"`
	DefaultModel string      `json:"default_model"`
	Models       []ModelInfo `json:"models"`
	APIKey       string      `json:"api_key"`
	ClearAPIKey  bool        `json:"clear_api_key"`
}

// Normalize trims and canonicalizes an input in place, then validates it. A
// provider may have no models; a non-empty default model must be configured.
func (in *ConfigInput) Normalize() error {
	if in == nil {
		return fmt.Errorf("%w: input is required", ErrInvalidConfig)
	}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidConfig)
	case utf8.RuneCountInString(in.Name) > maxNameLength:
		return fmt.Errorf("%w: name must be at most %d characters", ErrInvalidConfig, maxNameLength)
	}
	if !in.Format.Valid() {
		return fmt.Errorf("%w: format %q", ErrInvalidConfig, in.Format)
	}

	baseURL, err := normalizeBaseURL(in.BaseURL)
	if err != nil {
		return err
	}
	in.BaseURL = baseURL

	in.APIKey = strings.TrimSpace(in.APIKey)
	if in.ClearAPIKey && in.APIKey != "" {
		return fmt.Errorf("%w: cannot set and clear the API key at once", ErrInvalidConfig)
	}

	models := make([]ModelInfo, 0, len(in.Models))
	seen := make(map[string]struct{}, len(in.Models))
	for index, model := range in.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return fmt.Errorf("%w: model %d has no ID", ErrInvalidConfig, index)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: duplicate model %q", ErrInvalidConfig, id)
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		models = append(models, ModelInfo{ID: id, Name: name})
	}
	in.Models = models

	in.DefaultModel = strings.TrimSpace(in.DefaultModel)
	if in.DefaultModel == "" && len(models) > 0 {
		in.DefaultModel = models[0].ID
	}
	if in.DefaultModel != "" && !hasModel(models, in.DefaultModel) {
		return fmt.Errorf("%w: default model %q is not configured", ErrInvalidConfig, in.DefaultModel)
	}
	return nil
}

// Public returns a copy safe to serialize: the secret is cleared and
// APIKeySet reflects whether one was stored.
func (c Config) Public() Config {
	public := c
	public.APIKey = ""
	public.APIKeySet = c.APIKey != ""
	public.Models = slices.Clone(c.Models)
	return public
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: base URL: %w", ErrInvalidConfig, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: base URL must use http or https", ErrInvalidConfig)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: base URL must include a host", ErrInvalidConfig)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w: base URL must not include credentials", ErrInvalidConfig)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: base URL must not include a query or fragment", ErrInvalidConfig)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

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
	NewModel(ctx context.Context, modelID string) (llm.Model, error)
}

// ModelSource is the read-only seam the app uses: list choices and construct a
// model for one run.
type ModelSource interface {
	List() []Info
	NewModel(ctx context.Context, providerID, modelID string) (llm.Model, error)
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
func (r *Registry) NewModel(ctx context.Context, providerID, modelID string) (llm.Model, error) {
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
