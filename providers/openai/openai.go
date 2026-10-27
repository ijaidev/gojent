// Package openai provides OpenAI model providers implementing Gojent's core LLM
// contract: the Responses API and the Chat Completions API.
//
// The Responses API is item-oriented rather than message-oriented. The adapter
// maps canonical parts onto input items and back: assistant text becomes
// message items, reasoning parts become reasoning items with their encrypted
// content, function calls keep the item ID and correlation call ID separate,
// and hosted tool items are recorded verbatim and never executed locally.
//
// Replay mode sends the full canonical transcript. Server mode continues from
// a previous response ID and lets OpenAI retain conversation state.
//
// Chat Completions is stateless and message-oriented. ChatCompletionModel
// targets /chat/completions, including OpenAI-compatible local and hosted
// endpoints.
package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/internal/httpheader"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const providerName = "openai"

// Config defines initialization options for the OpenAI provider.
type Config struct {
	// Model is the Responses model identifier (for example "gpt-5" or
	// "o4-mini"). Required.
	Model string
	// APIKey is the OpenAI API key. When empty, the SDK resolves
	// OPENAI_API_KEY itself.
	APIKey string
	// BaseURL overrides the Responses API endpoint. It takes precedence over
	// OPENAI_BASE_URL.
	BaseURL string
	// Client is an optional pre-configured SDK client. When set, APIKey and
	// BaseURL are ignored.
	Client *sdk.Client
	// Headers are sent with every request. The map is copied at construction;
	// mutating it afterwards does not change the model.
	//
	// Request headers override SDK-generated defaults, including
	// Authorization. Do not put untrusted values here.
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
	// Reasoning optionally configures provider-level default reasoning
	// settings. Request-level settings take precedence.
	Reasoning *llm.ReasoningConfig
}

// Model implements the llm.Model and llm.Streamer interfaces using the OpenAI
// Responses API.
type Model struct {
	modelID    string
	client     sdk.Client
	headers    http.Header
	headerFunc func(ctx context.Context) (http.Header, error)
	reasoning  *llm.ReasoningConfig
}

var (
	_ llm.Model    = (*Model)(nil)
	_ llm.Streamer = (*Model)(nil)
)

// New creates a new OpenAI Responses model provider.
func New(_ context.Context, cfg Config) (*Model, error) {
	if cfg.Model == "" {
		return nil, ErrModelNotSet
	}
	headers, err := httpheader.Merge(cfg.Headers)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	var client sdk.Client
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
		client = sdk.NewClient(opts...)
	}

	return &Model{
		modelID:    cfg.Model,
		client:     client,
		headers:    headers,
		headerFunc: cfg.HeaderFunc,
		reasoning:  cfg.Reasoning.Clone(),
	}, nil
}

// ModelID returns the provider-facing model identifier.
func (m *Model) ModelID() string { return m.modelID }

// Info reports static model metadata and adapter capabilities.
func (m *Model) Info() llm.Info {
	return llm.Info{
		Provider: providerName,
		Model:    m.modelID,
		Features: llm.Features{
			Streaming:          true,
			Tools:              true,
			ParallelToolCalls:  true,
			StrictJSONSchema:   true,
			ServerState:        true,
			HostedTools:        true,
			MCP:                true,
			Reasoning:          true,
			ReasoningSummary:   true,
			ReasoningEncrypted: true,
			Vision:             true,
			AudioIn:            false,
			AudioOut:           false,
			Video:              false,
			Documents:          true,
			PromptCaching:      llm.CacheModeAutomatic,
			Citations:          false,
		},
	}
}

// Generate sends a non-streaming Responses request.
func (m *Model) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := m.requestParams(req)
	if err != nil {
		return nil, err
	}
	opts, err := m.headerOptions(ctx)
	if err != nil {
		return nil, err
	}

	response, err := m.client.Responses.New(ctx, params, opts...)
	if err != nil {
		return nil, wrapError(err)
	}
	return m.canonicalResponse(response)
}

// Stream initiates a streaming Responses request.
func (m *Model) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := m.requestParams(req)
	if err != nil {
		return nil, err
	}
	opts, err := m.headerOptions(ctx)
	if err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan llm.StreamEvent, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errCh)

		sdkStream := m.client.Responses.NewStreaming(streamCtx, params, opts...)
		defer func() {
			_ = sdkStream.Close()
		}()

		state := newStreamState(streamCtx, events, m.modelID)
		for sdkStream.Next() {
			if err := state.apply(sdkStream.Current()); err != nil {
				if streamCtx.Err() != nil {
					return
				}
				sendStreamError(streamCtx, errCh, err)
				return
			}
		}
		if err := sdkStream.Err(); err != nil {
			if streamCtx.Err() != nil {
				return
			}
			sendStreamError(streamCtx, errCh, wrapError(err))
			return
		}
		if streamCtx.Err() != nil {
			return
		}
		if err := state.finish(); err != nil {
			sendStreamError(streamCtx, errCh, err)
		}
	}()

	return llm.NewChannelStream(streamCtx, cancel, events, errCh), nil
}

// requestHeaders merges the model's static headers with the dynamic headers
// for this call.
func (m *Model) requestHeaders(ctx context.Context) (http.Header, error) {
	if m.headerFunc == nil {
		return m.headers, nil
	}
	dynamic, err := m.headerFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("openai: header function: %w", err)
	}
	headers, err := httpheader.Merge(m.headers, dynamic)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
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

func resolveAPIKey(cfg Config) string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	return os.Getenv("OPENAI_API_KEY")
}

func resolveBaseURL(cfg Config) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	return os.Getenv("OPENAI_BASE_URL")
}
