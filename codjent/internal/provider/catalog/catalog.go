// Package catalog persists provider configurations and exposes them through the
// provider.ModelSource seam.
//
// The catalog keeps an in-memory snapshot so List and NewModel never touch the
// database on the request path. Every create, update, or delete reloads the
// snapshot before returning.
package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/core/llm"
	anthropic "github.com/ijaidev/gojent/providers/anthropic"
	google "github.com/ijaidev/gojent/providers/google"
	openai "github.com/ijaidev/gojent/providers/openai"
	"google.golang.org/genai"
)

// Store is the persistence surface the catalog needs.
type Store interface {
	ListProviderConfigs(ctx context.Context) ([]*provider.Config, error)
	GetProviderConfig(ctx context.Context, id string) (*provider.Config, error)
	CreateProviderConfig(ctx context.Context, config *provider.Config) error
	UpdateProviderConfig(ctx context.Context, config *provider.Config) error
	DeleteProviderConfig(ctx context.Context, id string) error
}

// Catalog is a SQLite-backed provider.ModelSource.
type Catalog struct {
	store Store

	mu      sync.RWMutex
	configs []provider.Config
}

var _ provider.ModelSource = (*Catalog)(nil)

// New loads every persisted provider configuration.
func New(ctx context.Context, store Store) (*Catalog, error) {
	if store == nil {
		return nil, errors.New("catalog: store is required")
	}
	c := &Catalog{store: store}
	if err := c.Reload(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload replaces the snapshot with the persisted configurations.
func (c *Catalog) Reload(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadLocked(ctx)
}

// List implements provider.ModelSource. Providers without models are omitted
// because they cannot run a thread yet.
func (c *Catalog) List() []provider.Info {
	c.mu.RLock()
	defer c.mu.RUnlock()

	infos := make([]provider.Info, 0, len(c.configs))
	for _, config := range c.configs {
		if len(config.Models) == 0 {
			continue
		}
		infos = append(infos, provider.Info{
			ID:           config.ID,
			Name:         config.Name,
			DefaultModel: config.DefaultModel,
			Models:       slices.Clone(config.Models),
		})
	}
	return infos
}

// NewModel implements provider.ModelSource.
func (c *Catalog) NewModel(ctx context.Context, providerID, modelID string) (llm.Model, error) {
	config, resolvedModel, err := c.resolve(providerID, modelID)
	if err != nil {
		return nil, err
	}
	return newModel(ctx, config, resolvedModel)
}

// Configs returns every stored configuration with its secret masked.
func (c *Catalog) Configs() []provider.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()

	configs := make([]provider.Config, 0, len(c.configs))
	for _, config := range c.configs {
		configs = append(configs, config.Public())
	}
	return configs
}

// Create validates and persists a new provider configuration.
func (c *Catalog) Create(ctx context.Context, input provider.ConfigInput) (provider.Config, error) {
	if err := input.Normalize(); err != nil {
		return provider.Config{}, err
	}

	now := time.Now().UTC()
	config := &provider.Config{
		ID:           newID(),
		Name:         input.Name,
		Format:       input.Format,
		BaseURL:      input.BaseURL,
		DefaultModel: input.DefaultModel,
		Models:       slices.Clone(input.Models),
		APIKey:       input.APIKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.CreateProviderConfig(ctx, config); err != nil {
		return provider.Config{}, err
	}
	if err := c.reloadLocked(ctx); err != nil {
		return provider.Config{}, err
	}
	return config.Public(), nil
}

// Seed persists a provider configuration with a fixed ID. It is used to
// migrate the former static Google provider, whose ID is referenced by
// existing threads. An existing configuration is returned unchanged.
func (c *Catalog) Seed(ctx context.Context, id string, input provider.ConfigInput) (provider.Config, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return provider.Config{}, errors.New("catalog: seed ID is required")
	}
	if err := input.Normalize(); err != nil {
		return provider.Config{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	existing, err := c.store.GetProviderConfig(ctx, id)
	switch {
	case err == nil:
		return existing.Public(), nil
	case !errors.Is(err, provider.ErrConfigNotFound):
		return provider.Config{}, err
	}

	now := time.Now().UTC()
	config := &provider.Config{
		ID:           id,
		Name:         input.Name,
		Format:       input.Format,
		BaseURL:      input.BaseURL,
		DefaultModel: input.DefaultModel,
		Models:       slices.Clone(input.Models),
		APIKey:       input.APIKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := c.store.CreateProviderConfig(ctx, config); err != nil {
		return provider.Config{}, err
	}
	if err := c.reloadLocked(ctx); err != nil {
		return provider.Config{}, err
	}
	return config.Public(), nil
}

// Update replaces a provider configuration. An empty API key keeps the stored
// secret; ClearAPIKey removes it.
func (c *Catalog) Update(ctx context.Context, id string, input provider.ConfigInput) (provider.Config, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return provider.Config{}, fmt.Errorf("%w: ID is required", provider.ErrConfigNotFound)
	}
	if err := input.Normalize(); err != nil {
		return provider.Config{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	config, err := c.store.GetProviderConfig(ctx, id)
	if err != nil {
		return provider.Config{}, err
	}
	config.Name = input.Name
	config.Format = input.Format
	config.BaseURL = input.BaseURL
	config.DefaultModel = input.DefaultModel
	config.Models = slices.Clone(input.Models)
	switch {
	case input.ClearAPIKey:
		config.APIKey = ""
	case input.APIKey != "":
		config.APIKey = input.APIKey
	}
	config.UpdatedAt = time.Now().UTC()

	if err := c.store.UpdateProviderConfig(ctx, config); err != nil {
		return provider.Config{}, err
	}
	if err := c.reloadLocked(ctx); err != nil {
		return provider.Config{}, err
	}
	return config.Public(), nil
}

// Delete removes a provider configuration. Configurations referenced by a
// thread are kept so existing transcripts stay usable. The store enforces the
// reference check atomically with the delete.
func (c *Catalog) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: ID is required", provider.ErrConfigNotFound)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.store.DeleteProviderConfig(ctx, id); err != nil {
		return err
	}
	return c.reloadLocked(ctx)
}

func (c *Catalog) reloadLocked(ctx context.Context) error {
	configs, err := c.store.ListProviderConfigs(ctx)
	if err != nil {
		return err
	}
	snapshot := make([]provider.Config, len(configs))
	for index, config := range configs {
		if config == nil {
			return errors.New("catalog: store returned a nil provider configuration")
		}
		snapshot[index] = *config
		snapshot[index].Models = slices.Clone(config.Models)
	}
	c.configs = snapshot
	return nil
}

// resolve returns a copy of the configuration for one provider/model pair and
// the resolved model ID. Empty IDs select the first runnable provider and its
// default model.
func (c *Catalog) resolve(providerID, modelID string) (provider.Config, string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.configs) == 0 {
		return provider.Config{}, "", provider.ErrUnknownProvider
	}
	if providerID == "" {
		for _, config := range c.configs {
			if len(config.Models) > 0 {
				providerID = config.ID
				break
			}
		}
		if providerID == "" {
			return provider.Config{}, "", provider.ErrUnknownProvider
		}
	}

	index := slices.IndexFunc(c.configs, func(config provider.Config) bool {
		return config.ID == providerID
	})
	if index < 0 {
		return provider.Config{}, "", fmt.Errorf("%w: %s", provider.ErrUnknownProvider, providerID)
	}
	config := c.configs[index]
	if modelID == "" {
		modelID = config.DefaultModel
	}
	if !hasModel(config.Models, modelID) {
		return provider.Config{}, "", fmt.Errorf("%w: %s/%s", provider.ErrUnknownModel, providerID, modelID)
	}
	config.Models = slices.Clone(config.Models)
	return config, modelID, nil
}

func newModel(ctx context.Context, config provider.Config, modelID string) (llm.Model, error) {
	switch config.Format {
	case provider.FormatOpenAIResponses:
		return openai.New(ctx, openai.Config{
			Model:   modelID,
			APIKey:  config.APIKey,
			BaseURL: config.BaseURL,
		})
	case provider.FormatOpenAICompletion:
		return openai.NewChatCompletion(ctx, openai.ChatCompletionConfig{
			Model:   modelID,
			APIKey:  config.APIKey,
			BaseURL: config.BaseURL,
		})
	case provider.FormatAnthropic:
		return anthropic.New(ctx, anthropic.Config{
			Model:   modelID,
			APIKey:  config.APIKey,
			BaseURL: config.BaseURL,
		})
	case provider.FormatGoogle:
		googleConfig := google.Config{Model: modelID, APIKey: config.APIKey}
		if config.BaseURL != "" {
			googleConfig.ClientConfig = &genai.ClientConfig{
				HTTPOptions: genai.HTTPOptions{BaseURL: config.BaseURL},
			}
		}
		return google.New(ctx, googleConfig)
	default:
		return nil, fmt.Errorf("%w: format %q", provider.ErrInvalidConfig, config.Format)
	}
}

func hasModel(models []provider.ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("prv_%d", time.Now().UnixNano())
	}
	return "prv_" + hex.EncodeToString(b[:])
}
