// Package google provides a Google Gen AI model provider implementing Gojent's
// core LLM interface.
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/internal/httpheader"
	"google.golang.org/genai"
)

var (
	// ErrClientRequired is returned when client initialization fails or is invalid.
	ErrClientRequired = errors.New("google: failed to initialize genai client")

	// ErrModelNotSet is returned when no model identifier is provided in Config.
	ErrModelNotSet = errors.New("google: model is not set/provided")

	// ErrResponseBlocked is returned when Gemini blocks the prompt for safety or
	// policy reasons and produces no candidates, rather than a normal response.
	ErrResponseBlocked = errors.New("google: prompt blocked by safety or policy filters")
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
	// Headers are sent with every request. The map is copied at construction;
	// mutating it afterwards does not change the model.
	//
	// Request headers override ClientConfig.HTTPOptions.Headers with the same
	// name. They cannot override the x-goog-api-key header, which the SDK sets
	// after applying request headers.
	Headers http.Header
	// HeaderFunc returns additional headers for each model call. It runs once
	// per Generate or Stream call, after the request is validated and before
	// any network I/O. Returned values override Headers with the same name.
	// Returning an error fails the call.
	//
	// HeaderFunc may run concurrently for parallel runs sharing this model. It
	// must be safe for concurrent use and should derive per-run values from
	// ctx rather than captured state.
	HeaderFunc func(ctx context.Context) (http.Header, error)
	// Reasoning optionally configures provider-level default reasoning settings.
	Reasoning *llm.ReasoningConfig
	// ReasoningLevel optionally configures the provider-level default thinking
	// level (e.g. ThinkingLevelLow, ThinkingLevelHigh).
	ReasoningLevel ThinkingLevel
	// ReasoningBudget optionally configures the thinking token budget (0 to
	// disable thinking on supported models).
	ReasoningBudget *int
}

// Model implements the llm.Model and llm.Streamer interfaces using Google's
// Gen AI SDK.
type Model struct {
	modelID    string
	client     *genai.Client
	headers    http.Header
	headerFunc func(ctx context.Context) (http.Header, error)
	reasoning  *llm.ReasoningConfig
}

var (
	_ llm.Model    = (*Model)(nil)
	_ llm.Streamer = (*Model)(nil)
)

// New creates and initializes a new Google Gen AI model provider.
func New(ctx context.Context, cfg Config) (*Model, error) {
	modelID := cfg.Model
	if modelID == "" {
		return nil, ErrModelNotSet
	}
	headers, err := httpheader.Merge(cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
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

	reasoning := cfg.Reasoning.Clone()
	if cfg.ReasoningLevel != "" {
		if reasoning == nil {
			reasoning = &llm.ReasoningConfig{}
		}
		reasoning.Level = mapGenaiLevelToLLM(cfg.ReasoningLevel)
	}
	if cfg.ReasoningBudget != nil {
		if reasoning == nil {
			reasoning = &llm.ReasoningConfig{}
		}
		budget := *cfg.ReasoningBudget
		reasoning.BudgetTokens = &budget
	}

	return &Model{
		modelID:    modelID,
		client:     client,
		headers:    headers,
		headerFunc: cfg.HeaderFunc,
		reasoning:  reasoning,
	}, nil
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

// Info reports static model metadata. Limits and pricing are unknown here
// because they vary by model and deployment.
func (m *Model) Info() llm.Info {
	return llm.Info{
		Provider: providerName,
		Model:    m.modelID,
		Features: llm.Features{
			Streaming:         true,
			Tools:             true,
			ParallelToolCalls: true,
			StrictJSONSchema:  true,
			Reasoning:         true,
			ReasoningSummary:  true,
			Vision:            true,
			AudioIn:           true,
			Video:             true,
			Documents:         true,
			PromptCaching:     llm.CacheModeAutomatic,
		},
	}
}

// requestHeaders merges the model's static headers with the dynamic headers
// for this call.
func (m *Model) requestHeaders(ctx context.Context) (http.Header, error) {
	if m.headerFunc == nil {
		return m.headers, nil
	}
	dynamic, err := m.headerFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: header function: %w", err)
	}
	headers, err := httpheader.Merge(m.headers, dynamic)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	return headers, nil
}

// applyHeaders adds the request headers to a GenerateContent config. Values
// from HeaderFunc override static values with the same name.
func (m *Model) applyHeaders(ctx context.Context, cfg *genai.GenerateContentConfig) error {
	headers, err := m.requestHeaders(ctx)
	if err != nil {
		return err
	}
	if len(headers) == 0 {
		return nil
	}
	if cfg.HTTPOptions == nil {
		cfg.HTTPOptions = &genai.HTTPOptions{}
	}
	if cfg.HTTPOptions.Headers == nil {
		cfg.HTTPOptions.Headers = make(http.Header, len(headers))
	}
	for name, values := range headers {
		cfg.HTTPOptions.Headers[name] = append([]string(nil), values...)
	}
	return nil
}

// resolveReasoning merges request-level reasoning with provider defaults.
func (m *Model) resolveReasoning(req *llm.Request) *llm.ReasoningConfig {
	if req != nil && req.Reasoning != nil {
		return req.Reasoning
	}
	return m.reasoning
}
