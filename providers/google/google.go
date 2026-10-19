// Package google provides a Google Gen AI model provider implementing Gojent's core LLM interface.
package google

import (
	"context"
	"errors"
	"os"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

var (
	// ErrClientRequired is returned when client initialization fails or is invalid.
	ErrClientRequired = errors.New("google: failed to initialize genai client")

	// ErrModelNotSet is returned when no model identifier is provided in Config.
	ErrModelNotSet = errors.New("google: model is not set/provided")
)

// ThinkingLevel represents the reasoning effort level for Google Gen AI models.
type ThinkingLevel = genai.ThinkingLevel

const (
	// ThinkingLevelUnspecified indicates no preference; provider default is used.
	ThinkingLevelUnspecified ThinkingLevel = genai.ThinkingLevelUnspecified
	// ThinkingLevelMinimal requests minimal reasoning steps for lower latency and cost.
	ThinkingLevelMinimal ThinkingLevel = genai.ThinkingLevelMinimal
	// ThinkingLevelLow requests low reasoning depth.
	ThinkingLevelLow ThinkingLevel = genai.ThinkingLevelLow
	// ThinkingLevelMedium requests balanced reasoning depth (typical default on reasoning models).
	ThinkingLevelMedium ThinkingLevel = genai.ThinkingLevelMedium
	// ThinkingLevelHigh requests maximum reasoning depth for complex math, coding, and multi-step tasks.
	ThinkingLevelHigh ThinkingLevel = genai.ThinkingLevelHigh
)

// AvailableThinkingLevels returns all supported thinking levels for Google Gen AI models.
func AvailableThinkingLevels() []ThinkingLevel {
	return []ThinkingLevel{
		ThinkingLevelMinimal,
		ThinkingLevelLow,
		ThinkingLevelMedium,
		ThinkingLevelHigh,
	}
}

// Config defines initialization options for the Google Gen AI provider.
type Config struct {
	// Model is the Gemini model identifier (e.g. "gemini-2.5-flash", "gemini-1.5-pro"). Required.
	Model string
	// APIKey is the Google Gen AI or Gemini API key. It takes precedence over
	// ClientConfig.APIKey and the GEMINI_API_KEY / GOOGLE_API_KEY environment
	// variables. When all are empty, the client resolves the key from the
	// environment itself.
	APIKey string
	// Client is an optional pre-configured genai.Client instance.
	// When provided, APIKey and ClientConfig are ignored.
	Client *genai.Client
	// ClientConfig allows custom client options (e.g., custom HTTP options or Vertex AI backend).
	// It is copied before use, and its APIKey takes precedence over the
	// GEMINI_API_KEY / GOOGLE_API_KEY environment variables.
	ClientConfig *genai.ClientConfig
	// Thinking optionally configures provider-level default thinking settings.
	Thinking *llm.ThinkingConfig
	// ThinkingLevel optionally configures provider-level default thinking level (e.g. ThinkingLevelLow, ThinkingLevelHigh).
	ThinkingLevel ThinkingLevel
	// ThinkingBudget optionally configures the thinking token budget (legacy, 0 to disable thinking on supported models).
	ThinkingBudget *int32
}

// Model implements the llm.LLM interface using Google's Gen AI SDK.
type Model struct {
	modelID        string
	client         *genai.Client
	thinking       *llm.ThinkingConfig
	thinkingBudget *int32
}

// New creates and initializes a new Google Gen AI model provider.
func New(ctx context.Context, cfg Config) (*Model, error) {
	modelID := cfg.Model
	if modelID == "" {
		return nil, ErrModelNotSet
	}

	client := cfg.Client
	if client == nil {
		clientCfg := genai.ClientConfig{}
		if cfg.ClientConfig != nil {
			// Copy the caller's config: provider defaults must not mutate it.
			clientCfg = *cfg.ClientConfig
		}
		if apiKey := resolveAPIKey(cfg); apiKey != "" {
			clientCfg.APIKey = apiKey
		}

		var err error
		client, err = genai.NewClient(ctx, &clientCfg)
		if err != nil {
			return nil, err
		}
	}

	thinking := cfg.Thinking.Clone()
	if cfg.ThinkingLevel != "" {
		if thinking == nil {
			thinking = &llm.ThinkingConfig{}
		}
		thinking.Level = mapGenaiLevelToLLM(cfg.ThinkingLevel)
	}

	return &Model{
		modelID:        modelID,
		client:         client,
		thinking:       thinking,
		thinkingBudget: cfg.ThinkingBudget,
	}, nil
}

// NewModel is an alias for New.
func NewModel(ctx context.Context, cfg Config) (*Model, error) {
	return New(ctx, cfg)
}

// resolveAPIKey returns the API key used for a new client. Explicit
// configuration always beats the environment: the provider's APIKey field
// first, then ClientConfig.APIKey, then GEMINI_API_KEY, then GOOGLE_API_KEY.
func resolveAPIKey(cfg Config) string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	if cfg.ClientConfig != nil && cfg.ClientConfig.APIKey != "" {
		return cfg.ClientConfig.APIKey
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("GOOGLE_API_KEY")
}

// ModelID returns the identifier of the configured Gemini model.
func (m *Model) ModelID() string {
	return m.modelID
}

// resolveThinking resolves the effective thinking config taking into account request-level
// configuration and provider-level defaults.
func (m *Model) resolveThinking(req llm.GenerateRequest) *llm.ThinkingConfig {
	// 1. Request-level thinking configuration
	if req.Thinking != nil {
		return req.Thinking
	}

	// 2. Provider default
	if m.thinking != nil {
		return m.thinking
	}

	// 3. Legacy thinkingBudget
	if m.thinkingBudget != nil {
		b := int(*m.thinkingBudget)
		return &llm.ThinkingConfig{
			BudgetTokens: &b,
		}
	}

	return nil
}
