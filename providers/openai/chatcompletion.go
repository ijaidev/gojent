package openai

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/internal/httpheader"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
)

const chatProviderName = "openai-completion"

// ChatCompletionConfig defines initialization options for the OpenAI Chat
// Completions provider.
type ChatCompletionConfig struct {
	// Model is the Chat Completions model identifier. Required.
	Model string
	// APIKey is the OpenAI API key. When empty, the SDK resolves
	// OPENAI_API_KEY itself.
	APIKey string
	// BaseURL overrides the Chat Completions endpoint. It takes precedence
	// over OPENAI_BASE_URL.
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

// ChatCompletionModel implements llm.Model and llm.Streamer using the OpenAI
// Chat Completions API.
type ChatCompletionModel struct {
	modelID    string
	client     sdk.Client
	headers    http.Header
	headerFunc func(ctx context.Context) (http.Header, error)
	reasoning  *llm.ReasoningConfig
}

var (
	_ llm.Model    = (*ChatCompletionModel)(nil)
	_ llm.Streamer = (*ChatCompletionModel)(nil)
)

// NewChatCompletion creates a new Chat Completions model provider.
func NewChatCompletion(_ context.Context, cfg ChatCompletionConfig) (*ChatCompletionModel, error) {
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
		if key := resolveAPIKey(Config{APIKey: cfg.APIKey}); key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
		if baseURL := resolveBaseURL(Config{BaseURL: cfg.BaseURL}); baseURL != "" {
			opts = append(opts, option.WithBaseURL(baseURL))
		}
		client = sdk.NewClient(opts...)
	}

	return &ChatCompletionModel{
		modelID:    cfg.Model,
		client:     client,
		headers:    headers,
		headerFunc: cfg.HeaderFunc,
		reasoning:  cfg.Reasoning.Clone(),
	}, nil
}

// ModelID returns the provider-facing model identifier.
func (m *ChatCompletionModel) ModelID() string { return m.modelID }

// Info reports static model metadata and adapter capabilities.
func (m *ChatCompletionModel) Info() llm.Info {
	return llm.Info{
		Provider: chatProviderName,
		Model:    m.modelID,
		Features: llm.Features{
			Streaming:         true,
			Tools:             true,
			ParallelToolCalls: true,
			StrictJSONSchema:  true,
			Vision:            true,
			PromptCaching:     llm.CacheModeAutomatic,
			Reasoning:         true,
		},
	}
}

// Generate sends a non-streaming Chat Completions request.
func (m *ChatCompletionModel) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
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
	completion, err := m.client.Chat.Completions.New(ctx, params, opts...)
	if err != nil {
		return nil, wrapError(err)
	}
	return m.canonicalResponse(completion)
}

// Stream sends a streaming Chat Completions request.
func (m *ChatCompletionModel) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := m.requestParams(req)
	if err != nil {
		return nil, err
	}
	params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{
		IncludeUsage: param.NewOpt(true),
	}
	opts, err := m.headerOptions(ctx)
	if err != nil {
		return nil, err
	}

	stream := m.client.Chat.Completions.NewStreaming(ctx, params, opts...)
	return newChatCompletionStream(ctx, stream, m.modelID), nil
}

// requestHeaders merges the model's static headers with the dynamic headers
// for this call.
func (m *ChatCompletionModel) requestHeaders(ctx context.Context) (http.Header, error) {
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
func (m *ChatCompletionModel) headerOptions(ctx context.Context) ([]option.RequestOption, error) {
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

// requestParams builds one Chat Completions request. Every feature the adapter
// cannot honor fails with llm.ErrUnsupported instead of being dropped.
func (m *ChatCompletionModel) requestParams(req *llm.Request) (sdk.ChatCompletionNewParams, error) {
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.Validate(); err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	if err := validateChatState(req.State); err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	if raw, ok := req.ProviderOptions.Raw(chatProviderName); ok && len(raw) > 0 {
		return sdk.ChatCompletionNewParams{}, fmt.Errorf("%w: %s provider options", llm.ErrUnsupported, chatProviderName)
	}

	messages, err := convertChatMessages(req.Messages)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	params := sdk.ChatCompletionNewParams{
		Model:    m.modelID,
		Messages: messages,
	}

	tools, err := convertChatTools(req.Tools)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	params.Tools = tools
	params.ToolChoice, err = convertChatToolChoice(req.ToolChoice, req.Tools)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	params.ResponseFormat, err = convertChatResponseFormat(req.ResponseFormat)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}

	reasoning := m.reasoning
	if req.Reasoning != nil {
		reasoning = req.Reasoning
	}
	params.ReasoningEffort, err = chatReasoningEffort(reasoning)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}

	if req.Temperature != nil {
		if *req.Temperature < 0 || *req.Temperature > 2 {
			return sdk.ChatCompletionNewParams{}, fmt.Errorf("%w: temperature %v", llm.ErrUnsupported, *req.Temperature)
		}
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		if *req.TopP < 0 || *req.TopP > 1 {
			return sdk.ChatCompletionNewParams{}, fmt.Errorf("%w: top_p %v", llm.ErrUnsupported, *req.TopP)
		}
		params.TopP = param.NewOpt(*req.TopP)
	}
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 {
			return sdk.ChatCompletionNewParams{}, fmt.Errorf("%w: max tokens %d", llm.ErrUnsupported, *req.MaxTokens)
		}
		params.MaxTokens = param.NewOpt(int64(*req.MaxTokens))
	}
	if len(req.Stop) > 0 {
		params.Stop.OfStringArray = append([]string(nil), req.Stop...)
	}
	return params, nil
}

// validateChatState rejects server-managed state: Chat Completions is stateless.
func validateChatState(state llm.State) error {
	if state.EffectiveMode() != llm.StateModeReplay {
		return fmt.Errorf("%w: Chat Completions requires replay state", llm.ErrUnsupported)
	}
	if state.PreviousID != "" || state.Store != nil || len(state.Provider) > 0 {
		return fmt.Errorf("%w: Chat Completions cannot continue server state", llm.ErrUnsupported)
	}
	return nil
}

func chatReasoningEffort(reasoning *llm.ReasoningConfig) (shared.ReasoningEffort, error) {
	if reasoning == nil {
		return "", nil
	}
	if reasoning.BudgetTokens != nil {
		return "", fmt.Errorf("%w: Chat Completions reasoning budget", llm.ErrUnsupported)
	}
	if reasoning.IncludeThoughts != nil && *reasoning.IncludeThoughts {
		return "", fmt.Errorf("%w: Chat Completions cannot include reasoning thoughts", llm.ErrUnsupported)
	}
	switch reasoning.Level {
	case "":
		return "", nil
	case llm.ReasoningLevelMinimal:
		return shared.ReasoningEffortLow, nil
	case llm.ReasoningLevelLow:
		return shared.ReasoningEffortLow, nil
	case llm.ReasoningLevelMedium:
		return shared.ReasoningEffortMedium, nil
	case llm.ReasoningLevelHigh:
		return shared.ReasoningEffortHigh, nil
	case llm.ReasoningLevelNone:
		return "", fmt.Errorf("%w: Chat Completions cannot disable reasoning", llm.ErrUnsupported)
	default:
		return "", fmt.Errorf("%w: reasoning level %q", llm.ErrUnsupported, reasoning.Level)
	}
}
