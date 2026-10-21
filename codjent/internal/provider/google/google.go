// Package googleprovider adapts the gojent Google Gen AI provider to codjent's
// provider registry.
package googleprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/core/llm"
	google "github.com/ijaidev/gojent/providers/google"
)

const (
	// ID is the registry identifier for Google Gemini.
	ID = "google"
	// DefaultModel is used when no model is configured.
	DefaultModel = "gemini-3.8-flash"
)

// Config configures the Google provider adapter.
type Config struct {
	Models       []string
	DefaultModel string
	// APIKey is optional. When empty, the gojent provider resolves
	// GEMINI_API_KEY or GOOGLE_API_KEY from the environment.
	APIKey string
}

// Provider exposes the configured Gemini models.
type Provider struct {
	models       []provider.ModelInfo
	defaultModel string
	apiKey       string
}

// New creates a Google provider. Empty model lists fall back to DefaultModel.
func New(cfg Config) *Provider {
	ids := normalizeModels(cfg.Models)
	if len(ids) == 0 {
		ids = []string{DefaultModel}
	}

	defaultModel := strings.TrimSpace(cfg.DefaultModel)
	if defaultModel == "" {
		defaultModel = ids[0]
	}
	if !slices.Contains(ids, defaultModel) {
		ids = append([]string{defaultModel}, ids...)
	}

	models := make([]provider.ModelInfo, len(ids))
	for i, id := range ids {
		models[i] = provider.ModelInfo{ID: id, Name: displayName(id)}
	}
	return &Provider{
		models:       models,
		defaultModel: defaultModel,
		apiKey:       cfg.APIKey,
	}
}

// Info implements provider.Provider.
func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:           ID,
		Name:         "Google Gemini",
		DefaultModel: p.defaultModel,
		Models:       slices.Clone(p.models),
	}
}

// NewModel implements provider.Provider.
func (p *Provider) NewModel(ctx context.Context, modelID string) (llm.LLM, error) {
	if modelID == "" {
		modelID = p.defaultModel
	}
	if !hasModel(p.models, modelID) {
		return nil, fmt.Errorf("%w: %s", provider.ErrUnknownModel, modelID)
	}
	if p.apiKey == "" && !hasGoogleAPIKey() {
		return nil, errors.New("api key is not configured; set GEMINI_API_KEY or GOOGLE_API_KEY")
	}
	return google.New(ctx, google.Config{Model: modelID, APIKey: p.apiKey})
}

func hasGoogleAPIKey() bool {
	return strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" ||
		strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
}

func normalizeModels(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func hasModel(models []provider.ModelInfo, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func displayName(id string) string {
	switch id {
	case "gemini-3.8-flash":
		return "Gemini 3.8 Flash"
	case "gemini-2.5-flash":
		return "Gemini 2.5 Flash"
	case "gemini-1.5-pro":
		return "Gemini 1.5 Pro"
	default:
		return id
	}
}
