// Package anthropic provides an Anthropic Messages API model provider
// implementing Gojent's core LLM contract.
//
// The adapter is stateless: every request replays the canonical transcript. The
// Messages API has no server-side conversation state, so Request.State must be
// replay mode. Thinking blocks are replayed with their exact signatures, and
// tool results travel as tool_result blocks inside user messages rather than a
// separate tool role.
package anthropic

import (
	"context"
	"errors"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ijaidev/gojent/core/llm"
)

const providerName = "anthropic"

// DefaultMaxTokens is used when a request does not set MaxTokens. The Messages
// API requires a max_tokens value on every request.
const DefaultMaxTokens = 4096

var (
	// ErrModelNotSet is returned when Config.Model is empty.
	ErrModelNotSet = errors.New("anthropic: model is not set")
	// ErrMessagesRequired is returned when the transcript has no user or
	// assistant message to send.
	ErrMessagesRequired = errors.New("anthropic: at least one user or assistant message is required")
)

// Config defines initialization options for the Anthropic provider.
type Config struct {
	// Model is the Claude model identifier (for example
	// "claude-sonnet-4-5"). Required.
	Model string
	// APIKey is the Anthropic API key. When empty, the SDK resolves
	// ANTHROPIC_API_KEY (and its other credential sources) itself.
	APIKey string
	// BaseURL overrides the Messages API endpoint. It takes precedence over
	// ANTHROPIC_BASE_URL.
	BaseURL string
	// Client is an optional pre-configured SDK client. When set, APIKey and
	// BaseURL are ignored.
	Client *anthropic.Client
	// MaxTokens is the default max_tokens for requests that do not set one.
	// Zero means DefaultMaxTokens.
	MaxTokens int
	// Reasoning optionally configures provider-level default reasoning
	// settings. Request-level settings take precedence.
	Reasoning *llm.ReasoningConfig
}

// Model implements the llm.Model and llm.Streamer interfaces using the
// Anthropic Messages API.
type Model struct {
	modelID   string
	client    anthropic.Client
	maxTokens int
	reasoning *llm.ReasoningConfig
}

var (
	_ llm.Model    = (*Model)(nil)
	_ llm.Streamer = (*Model)(nil)
)

// New creates a new Anthropic Messages model provider.
func New(_ context.Context, cfg Config) (*Model, error) {
	if cfg.Model == "" {
		return nil, ErrModelNotSet
	}

	var client anthropic.Client
	if cfg.Client != nil {
		client = *cfg.Client
	} else {
		var opts []option.RequestOption
		if key := resolveAPIKey(cfg); key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
		if baseURL := resolveBaseURL(cfg); baseURL != "" {
			opts = append(opts, option.WithBaseURL(baseURL))
		}
		client = anthropic.NewClient(opts...)
	}

	maxTokens := cfg.MaxTokens
	if maxTokens < 0 {
		return nil, errors.New("anthropic: max tokens must not be negative")
	}
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}

	return &Model{
		modelID:   cfg.Model,
		client:    client,
		maxTokens: maxTokens,
		reasoning: cfg.Reasoning.Clone(),
	}, nil
}

// resolveAPIKey returns the configured key, falling back to the environment
// variable the SDK itself reads.
func resolveAPIKey(cfg Config) string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	return os.Getenv("ANTHROPIC_API_KEY")
}

// resolveBaseURL returns the configured endpoint, falling back to the
// environment variable the SDK itself reads.
func resolveBaseURL(cfg Config) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	return os.Getenv("ANTHROPIC_BASE_URL")
}

// ModelID returns the configured Claude model identifier.
func (m *Model) ModelID() string {
	return m.modelID
}

// Info reports static model metadata. Limits and pricing are unknown because
// they vary by model and deployment.
//
// StrictJSONSchema is false: the runner uses that bit to gate response-format
// requests, and Messages has no response format. Strict tool input schemas are
// still forwarded per definition.
func (m *Model) Info() llm.Info {
	return llm.Info{
		Provider: providerName,
		Model:    m.modelID,
		Features: llm.Features{
			Streaming:          true,
			Tools:              true,
			ParallelToolCalls:  true,
			StrictJSONSchema:   false,
			ServerState:        false,
			HostedTools:        false,
			MCP:                false,
			Reasoning:          true,
			ReasoningSummary:   false,
			ReasoningEncrypted: true,
			Vision:             true,
			AudioIn:            false,
			AudioOut:           false,
			Video:              false,
			Documents:          true,
			PromptCaching:      llm.CacheModeNone,
			Citations:          false,
		},
	}
}

// resolveReasoning merges request-level reasoning with provider defaults.
func (m *Model) resolveReasoning(req *llm.Request) *llm.ReasoningConfig {
	if req != nil && req.Reasoning != nil {
		return req.Reasoning
	}
	return m.reasoning
}
