// Package google provides a Google Gen AI model provider implementing Gojent's core LLM interface.
package google

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

var (
	// ErrClientRequired is returned when client initialization fails or is invalid.
	ErrClientRequired = errors.New("google: failed to initialize genai client")

	// ErrModelNotSet is returned when no model identifier is provided in Config.
	ErrModelNotSet = errors.New("google: model is not set/provided")
)

// Config defines initialization options for the Google Gen AI provider.
type Config struct {
	// Model is the Gemini model identifier (e.g. "gemini-2.5-flash", "gemini-1.5-pro"). Required.
	Model string
	// APIKey is the Google Gen AI or Gemini API key.
	// If empty, the client resolves it from the GEMINI_API_KEY or GOOGLE_API_KEY environment variables.
	APIKey string
	// Client is an optional pre-configured genai.Client instance.
	// When provided, APIKey and ClientConfig are ignored.
	Client *genai.Client
	// ClientConfig allows custom client options (e.g., custom HTTP options or Vertex AI backend).
	ClientConfig *genai.ClientConfig
}

// Model implements the llm.LLM interface using Google's Gen AI SDK.
type Model struct {
	modelID string
	client  *genai.Client
}

// New creates and initializes a new Google Gen AI model provider.
func New(ctx context.Context, cfg Config) (*Model, error) {
	modelID := cfg.Model
	if modelID == "" {
		return nil, ErrModelNotSet
	}

	client := cfg.Client
	if client == nil {
		clientCfg := cfg.ClientConfig
		if clientCfg == nil {
			clientCfg = &genai.ClientConfig{}
		}

		apiKey := cfg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GOOGLE_API_KEY")
			}
		}
		if apiKey != "" {
			clientCfg.APIKey = apiKey
		}

		var err error
		client, err = genai.NewClient(ctx, clientCfg)
		if err != nil {
			return nil, err
		}
	}

	return &Model{
		modelID: modelID,
		client:  client,
	}, nil
}

// NewModel is an alias for New.
func NewModel(ctx context.Context, cfg Config) (*Model, error) {
	return New(ctx, cfg)
}

// ModelID returns the identifier of the configured Gemini model.
func (m *Model) ModelID() string {
	return m.modelID
}

// Generate sends a non-streaming completion request to the Gemini model.
func (m *Model) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	contents, systemInstruction := convertMessages(req.Messages)
	config := convertConfig(req, systemInstruction)

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, err
	}

	text := resp.Text()
	finishReason := llm.FinishReasonStop
	if len(resp.Candidates) > 0 && resp.Candidates[0].FinishReason != "" {
		finishReason = mapFinishReason(resp.Candidates[0].FinishReason)
	}

	var usage llm.Usage
	if resp.UsageMetadata != nil {
		usage = llm.Usage{
			PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return &llm.GenerateResponse{
		Text:         text,
		FinishReason: finishReason,
		Usage:        usage,
		Raw:          resp,
	}, nil
}

// Stream initiates a streaming generation from the Gemini model, returning token deltas via llm.Stream.
func (m *Model) Stream(ctx context.Context, req llm.GenerateRequest) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	contents, systemInstruction := convertMessages(req.Messages)
	config := convertConfig(req, systemInstruction)

	streamCtx, cancel := context.WithCancel(ctx)
	ch := make(chan *llm.StreamChunk)
	errCh := make(chan error, 1)

	go func() {
		defer close(ch)
		defer close(errCh)

		iterSeq := m.client.Models.GenerateContentStream(streamCtx, m.modelID, contents, config)
		for resp, err := range iterSeq {
			if err != nil {
				select {
				case errCh <- err:
				case <-streamCtx.Done():
				}
				return
			}

			deltaText := resp.Text()
			var finishReason llm.FinishReason
			var usage *llm.Usage

			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.FinishReason != "" {
					finishReason = mapFinishReason(cand.FinishReason)
				}
			}

			if resp.UsageMetadata != nil {
				usage = &llm.Usage{
					PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
					CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
					TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
				}
			}

			chunk := &llm.StreamChunk{
				Delta:        deltaText,
				FinishReason: finishReason,
				Usage:        usage,
				Raw:          resp,
			}

			select {
			case ch <- chunk:
			case <-streamCtx.Done():
				return
			}
		}
	}()

	return llm.NewChannelStream(streamCtx, cancel, ch, errCh), nil
}

// convertMessages translates Gojent messages to Google Gen AI contents and extracts system instructions.
func convertMessages(messages []llm.Message) (contents []*genai.Content, systemInstruction *genai.Content) {
	var systemParts []*genai.Part

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleSystem:
			if msg.Content != "" {
				systemParts = append(systemParts, &genai.Part{Text: msg.Content})
			}
		case llm.RoleAssistant:
			contents = append(contents, &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: msg.Content}},
			})
		case llm.RoleUser:
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: []*genai.Part{{Text: msg.Content}},
			})
		case llm.RoleTool:
			contents = append(contents, &genai.Content{
				Role:  "tool",
				Parts: []*genai.Part{{Text: msg.Content}},
			})
		default:
			contents = append(contents, &genai.Content{
				Role:  string(msg.Role),
				Parts: []*genai.Part{{Text: msg.Content}},
			})
		}
	}

	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{
			Parts: systemParts,
		}
	}

	return contents, systemInstruction
}

// convertConfig prepares the genai.GenerateContentConfig from Gojent request parameters.
func convertConfig(req llm.GenerateRequest, systemInstruction *genai.Content) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}

	if req.Temperature != nil {
		t := float32(*req.Temperature)
		cfg.Temperature = &t
	}

	if req.TopP != nil {
		p := float32(*req.TopP)
		cfg.TopP = &p
	}

	if req.MaxTokens != nil {
		cfg.MaxOutputTokens = int32(*req.MaxTokens)
	}

	if len(req.StopWords) > 0 {
		cfg.StopSequences = req.StopWords
	}

	return cfg
}

// mapFinishReason maps Gen AI finish reason strings to standard Gojent finish reasons.
func mapFinishReason(fr genai.FinishReason) llm.FinishReason {
	switch fr {
	case genai.FinishReasonStop:
		return llm.FinishReasonStop
	case genai.FinishReasonMaxTokens:
		return llm.FinishReasonLength
	case genai.FinishReasonUnexpectedToolCall:
		return llm.FinishReasonTool
	default:
		return llm.FinishReason(strings.ToLower(string(fr)))
	}
}
