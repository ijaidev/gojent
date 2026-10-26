// Package openai provides an OpenAI Responses API model provider implementing
// Gojent's core LLM contract.
//
// The Responses API is item-oriented rather than message-oriented. The adapter
// maps canonical parts onto input items and back: assistant text becomes
// message items, reasoning parts become reasoning items with their encrypted
// content, function calls keep the item ID and correlation call ID separate,
// and hosted tool items are recorded verbatim and never executed locally.
//
// Replay mode sends the full canonical transcript. Server mode continues from
// a previous response ID and lets OpenAI retain conversation state.
package openai

import (
	"context"
	"os"

	"github.com/ijaidev/gojent/core/llm"
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
	// Reasoning optionally configures provider-level default reasoning
	// settings. Request-level settings take precedence.
	Reasoning *llm.ReasoningConfig
}

// Model implements the llm.Model and llm.Streamer interfaces using the OpenAI
// Responses API.
type Model struct {
	modelID   string
	client    sdk.Client
	reasoning *llm.ReasoningConfig
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
		modelID:   cfg.Model,
		client:    client,
		reasoning: cfg.Reasoning.Clone(),
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

	response, err := m.client.Responses.New(ctx, params)
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

	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan llm.StreamEvent, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errCh)

		sdkStream := m.client.Responses.NewStreaming(streamCtx, params)
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
