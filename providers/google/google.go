// Package google provides a Google Gen AI model provider implementing Gojent's core LLM interface.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// ThinkingBudget optionally configures the thinking token budget (0 to disable thinking on supported models).
	ThinkingBudget *int32
}

// Model implements the llm.LLM interface using Google's Gen AI SDK.
type Model struct {
	modelID        string
	client         *genai.Client
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
		modelID:        modelID,
		client:         client,
		thinkingBudget: cfg.ThinkingBudget,
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
	config := m.convertConfig(req, systemInstruction)

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, err
	}

	text := extractCandidateText(resp.Candidates)
	finishReason := llm.FinishReasonStop
	if len(resp.Candidates) > 0 && resp.Candidates[0].FinishReason != "" {
		finishReason = mapFinishReason(resp.Candidates[0].FinishReason)
	}

	var toolCalls []llm.ToolCall
	for _, fc := range resp.FunctionCalls() {
		argsBytes, _ := json.Marshal(fc.Args)
		callID := fc.ID
		if callID == "" {
			callID = fmt.Sprintf("call_%s", fc.Name)
		}
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        callID,
			Name:      fc.Name,
			Arguments: string(argsBytes),
		})
	}
	if len(toolCalls) > 0 && finishReason == llm.FinishReasonStop {
		finishReason = llm.FinishReasonTool
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
		ToolCalls:    toolCalls,
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
	config := m.convertConfig(req, systemInstruction)

	streamCtx, cancel := context.WithCancel(ctx)
	ch := make(chan *llm.StreamChunk)
	errCh := make(chan error, 1)

	go func() {
		defer close(ch)
		defer close(errCh)

		var accumulatedParts []*genai.Part
		iterSeq := m.client.Models.GenerateContentStream(streamCtx, m.modelID, contents, config)
		for resp, err := range iterSeq {
			if err != nil {
				select {
				case errCh <- err:
				case <-streamCtx.Done():
				}
				return
			}

			deltaText := extractCandidateText(resp.Candidates)
			var finishReason llm.FinishReason
			var usage *llm.Usage

			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.Content != nil {
					for _, p := range cand.Content.Parts {
						if isPartValid(p) {
							accumulatedParts = append(accumulatedParts, p)
						}
					}
				}
				if cand.FinishReason != "" {
					finishReason = mapFinishReason(cand.FinishReason)
				}
			}

			var toolCalls []llm.ToolCall
			for _, fc := range resp.FunctionCalls() {
				argsBytes, _ := json.Marshal(fc.Args)
				callID := fc.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%s", fc.Name)
				}
				toolCalls = append(toolCalls, llm.ToolCall{
					ID:        callID,
					Name:      fc.Name,
					Arguments: string(argsBytes),
				})
			}
			if len(toolCalls) > 0 && (finishReason == "" || finishReason == llm.FinishReasonStop) {
				finishReason = llm.FinishReasonTool
			}

			if resp.UsageMetadata != nil {
				usage = &llm.Usage{
					PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
					CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
					TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
				}
			}

			var rawContent *genai.Content
			if len(accumulatedParts) > 0 {
				rawContent = &genai.Content{
					Role:  genai.RoleModel,
					Parts: append([]*genai.Part(nil), accumulatedParts...),
				}
			}

			chunk := &llm.StreamChunk{
				Delta:        deltaText,
				ToolCalls:    toolCalls,
				FinishReason: finishReason,
				Usage:        usage,
				Raw:          rawContent,
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
			// If we have the original raw Gemini response content, preserve it directly.
			// This preserves all thought blocks, thought signatures, and function call metadata
			// required by Gemini 2.5+ models in multi-turn interactions.
			if rawContent := extractRawGeminiContent(msg.Raw); rawContent != nil {
				contents = append(contents, rawContent)
				continue
			}

			var parts []*genai.Part
			if msg.Content != "" {
				parts = append(parts, &genai.Part{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var argsMap map[string]any
				if tc.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Arguments), &argsMap)
				}
				if argsMap == nil {
					argsMap = make(map[string]any)
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   tc.ID,
						Name: tc.Name,
						Args: argsMap,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  genai.RoleModel,
					Parts: parts,
				})
			}
		case llm.RoleTool:
			var respMap map[string]any
			if msg.Content != "" {
				if err := json.Unmarshal([]byte(msg.Content), &respMap); err != nil {
					respMap = map[string]any{"output": msg.Content}
				}
			} else {
				respMap = map[string]any{"output": ""}
			}
			name := msg.Name
			if name == "" {
				name = msg.ToolCallID
			}
			part := &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       msg.ToolCallID,
					Name:     name,
					Response: respMap,
				},
			}
			// Gemini requires function responses to have role "user".
			// If the immediately preceding content is already a user turn with function responses,
			// merge parts into it to maintain strictly alternating turns (user -> model -> user).
			if len(contents) > 0 && contents[len(contents)-1].Role == genai.RoleUser &&
				len(contents[len(contents)-1].Parts) > 0 &&
				contents[len(contents)-1].Parts[0].FunctionResponse != nil {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, part)
			} else {
				contents = append(contents, &genai.Content{
					Role:  genai.RoleUser,
					Parts: []*genai.Part{part},
				})
			}
		default:
			if msg.Content != "" {
				contents = append(contents, &genai.Content{
					Role:  string(msg.Role),
					Parts: []*genai.Part{{Text: msg.Content}},
				})
			}
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
func (m *Model) convertConfig(req llm.GenerateRequest, systemInstruction *genai.Content) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}

	if m.thinkingBudget != nil {
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: m.thinkingBudget,
		}
	}

	if len(req.Tools) > 0 {
		var decls []*genai.FunctionDeclaration
		for _, t := range req.Tools {
			decl := &genai.FunctionDeclaration{
				Name:                 t.Name,
				Description:          t.Description,
				ParametersJsonSchema: t.Parameters,
			}
			decls = append(decls, decl)
		}
		cfg.Tools = []*genai.Tool{
			{
				FunctionDeclarations: decls,
			},
		}
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

// extractCandidateText retrieves text parts across candidates without logging warnings on function calls.
func extractCandidateText(candidates []*genai.Candidate) string {
	var sb strings.Builder
	for _, cand := range candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					sb.WriteString(part.Text)
				}
			}
		}
	}
	return sb.String()
}

// extractRawGeminiContent attempts to retrieve the original *genai.Content from a raw response payload,
// filtering out any empty parts that lack initialized 'data' fields required by the Gemini API.
func extractRawGeminiContent(raw any) *genai.Content {
	if raw == nil {
		return nil
	}
	var rawParts []*genai.Part
	var role string
	switch r := raw.(type) {
	case *genai.GenerateContentResponse:
		if len(r.Candidates) > 0 && r.Candidates[0].Content != nil {
			role = r.Candidates[0].Content.Role
			rawParts = r.Candidates[0].Content.Parts
		}
	case *genai.Content:
		role = r.Role
		rawParts = r.Parts
	}

	if len(rawParts) == 0 {
		return nil
	}

	var validParts []*genai.Part
	for _, p := range rawParts {
		if isPartValid(p) {
			validParts = append(validParts, p)
		}
	}
	if len(validParts) == 0 {
		return nil
	}

	if role == "" {
		role = genai.RoleModel
	}
	return &genai.Content{
		Role:  role,
		Parts: validParts,
	}
}

// isPartValid returns true if the Part contains an initialized payload for the 'data' oneof.
func isPartValid(p *genai.Part) bool {
	if p == nil {
		return false
	}
	return p.Text != "" ||
		p.FunctionCall != nil ||
		p.FunctionResponse != nil ||
		p.InlineData != nil ||
		p.FileData != nil ||
		p.ExecutableCode != nil ||
		p.CodeExecutionResult != nil
}
