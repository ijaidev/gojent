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
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/internal/httpheader"
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
	// Headers are sent with every request. The map is copied at construction;
	// mutating it afterwards does not change the model.
	//
	// Request headers override SDK-generated defaults, including
	// Authorization and X-Api-Key. Do not put untrusted values here.
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
	modelID    string
	client     anthropic.Client
	headers    http.Header
	headerFunc func(ctx context.Context) (http.Header, error)
	maxTokens  int
	reasoning  *llm.ReasoningConfig
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
	headers, err := httpheader.Merge(cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
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
		modelID:    cfg.Model,
		client:     client,
		headers:    headers,
		headerFunc: cfg.HeaderFunc,
		maxTokens:  maxTokens,
		reasoning:  cfg.Reasoning.Clone(),
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

// requestHeaders merges the model's static headers with the dynamic headers
// for this call.
func (m *Model) requestHeaders(ctx context.Context) (http.Header, error) {
	if m.headerFunc == nil {
		return m.headers, nil
	}
	dynamic, err := m.headerFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("anthropic: header function: %w", err)
	}
	headers, err := httpheader.Merge(m.headers, dynamic)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	return headers, nil
}

// headerOptions converts headers into SDK request options. Names are sorted so
// the generated option list is deterministic.
func (m *Model) headerOptions(ctx context.Context) ([]option.RequestOption, error) {
	headers, err := m.requestHeaders(ctx)
	if err != nil {
		return nil, err
	}
	if len(headers) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)

	opts := make([]option.RequestOption, 0, len(headers))
	for _, name := range names {
		values := headers[name]
		opts = append(opts, option.WithHeader(name, values[0]))
		for _, value := range values[1:] {
			opts = append(opts, option.WithHeaderAdd(name, value))
		}
	}
	return opts, nil
}

// resolveReasoning merges request-level reasoning with provider defaults.
func (m *Model) resolveReasoning(req *llm.Request) *llm.ReasoningConfig {
	if req != nil && req.Reasoning != nil {
		return req.Reasoning
	}
	return m.reasoning
}
