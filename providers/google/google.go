// Package google provides a Google Gen AI model provider implementing Gojent's core LLM interface.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
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
	// APIKey is the Google Gen AI or Gemini API key.
	// If empty, the client resolves it from the GEMINI_API_KEY or GOOGLE_API_KEY environment variables.
	APIKey string
	// Client is an optional pre-configured genai.Client instance.
	// When provided, APIKey and ClientConfig are ignored.
	Client *genai.Client
	// ClientConfig allows custom client options (e.g., custom HTTP options or Vertex AI backend).
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

// Generate sends a non-streaming completion request to the Gemini model.
func (m *Model) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config := m.convertConfig(req, systemInstruction)

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, err
	}

	text, thinking := extractCandidateTextAndThinking(resp.Candidates)
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
			ReasoningTokens:  int(resp.UsageMetadata.ThoughtsTokenCount),
		}
	}

	return &llm.GenerateResponse{
		Text:         text,
		Thinking:     thinking,
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

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
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

			var deltaText string
			var deltaThinking string
			var finishReason llm.FinishReason
			var usage *llm.Usage

			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.Content != nil {
					for _, p := range cand.Content.Parts {
						if isPartValid(p) {
							accumulatedParts = append(accumulatedParts, p)
						}
						if p.Thought {
							deltaThinking += p.Text
						} else if p.Text != "" {
							deltaText += p.Text
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
					ReasoningTokens:  int(resp.UsageMetadata.ThoughtsTokenCount),
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
				Delta:         deltaText,
				ThinkingDelta: deltaThinking,
				ToolCalls:     toolCalls,
				FinishReason:  finishReason,
				Usage:         usage,
				Raw:           rawContent,
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
func convertMessages(messages []llm.Message) (contents []*genai.Content, systemInstruction *genai.Content, err error) {
	var systemParts []*genai.Part

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleSystem:
			for _, p := range msg.Content {
				if p.Type == llm.PartTypeText {
					systemParts = append(systemParts, genai.NewPartFromText(p.Text))
				} else {
					return nil, nil, fmt.Errorf("google: system message only supports text parts: %w", llm.ErrUnsupportedPartType)
				}
			}
		case llm.RoleAssistant:
			if rawContent := extractRawGeminiContent(msg.Raw); rawContent != nil {
				contents = append(contents, rawContent)
				continue
			}

			var parts []*genai.Part
			for _, p := range msg.Content {
				gp, convErr := convertPartToGemini(p)
				if convErr != nil {
					return nil, nil, convErr
				}
				parts = append(parts, gp)
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
			name := msg.Name
			if name == "" {
				name = msg.ToolCallID
			}

			var functionResponseParts []*genai.FunctionResponsePart
			var respMap map[string]any

			for _, p := range msg.Content {
				if p.Type == llm.PartTypeText {
					if respMap == nil {
						if err := json.Unmarshal([]byte(p.Text), &respMap); err != nil {
							respMap = map[string]any{"output": p.Text}
						}
					}
				} else {
					frp, convErr := convertToolPartToGemini(p)
					if convErr != nil {
						return nil, nil, convErr
					}
					functionResponseParts = append(functionResponseParts, frp)
				}
			}

			if respMap == nil {
				respMap = map[string]any{"output": ""}
			}

			var part *genai.Part
			if len(functionResponseParts) > 0 {
				part = genai.NewPartFromFunctionResponseWithParts(name, respMap, functionResponseParts)
				if part != nil && part.FunctionResponse != nil {
					part.FunctionResponse.ID = msg.ToolCallID
				}
			} else {
				part = genai.NewPartFromFunctionResponse(name, respMap)
				if part != nil && part.FunctionResponse != nil {
					part.FunctionResponse.ID = msg.ToolCallID
				}
			}

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
			var parts []*genai.Part
			for _, p := range msg.Content {
				gp, convErr := convertPartToGemini(p)
				if convErr != nil {
					return nil, nil, convErr
				}
				parts = append(parts, gp)
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  string(msg.Role),
					Parts: parts,
				})
			}
		}
	}

	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{
			Parts: systemParts,
		}
	}

	return contents, systemInstruction, nil
}

// convertPartToGemini translates an individual Gojent llm.Part to a Google Gen AI genai.Part.
func convertPartToGemini(p llm.Part) (*genai.Part, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	switch p.Type {
	case llm.PartTypeText:
		return genai.NewPartFromText(p.Text), nil

	case llm.PartTypeImage:
		mimeType := p.MIMEType
		data := p.Data
		if len(data) == 0 && p.Path != "" {
			var err error
			data, err = os.ReadFile(p.Path)
			if err != nil {
				return nil, fmt.Errorf("google: failed to read image from %q: %w", p.Path, err)
			}
			if mimeType == "" {
				mimeType = detectMIMEType(p.Path, data)
			}
		}

		if mimeType != "" && !isSupportedImageMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported image MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "image/png"
		}

		if len(data) > 0 {
			return genai.NewPartFromBytes(data, mimeType), nil
		}

		if p.URL != "" {
			if strings.HasPrefix(p.URL, "gs://") || strings.HasPrefix(p.URL, "https://generativelanguage.googleapis.com/") {
				return genai.NewPartFromURI(p.URL, mimeType), nil
			}
			return nil, fmt.Errorf("google: direct HTTP/HTTPS URL %q not supported by Gemini API (upload via Files API or provide inline bytes): %w", p.URL, llm.ErrUnsupportedPart)
		}

		return nil, fmt.Errorf("google: image part must provide Data, URL, or Path: %w", llm.ErrInvalidPart)

	case llm.PartTypeAudio:
		mimeType := p.MIMEType
		data := p.Data
		if len(data) == 0 && p.Path != "" {
			var err error
			data, err = os.ReadFile(p.Path)
			if err != nil {
				return nil, fmt.Errorf("google: failed to read audio from %q: %w", p.Path, err)
			}
			if mimeType == "" {
				mimeType = detectMIMEType(p.Path, data)
			}
		}

		if mimeType != "" && !isSupportedAudioMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported audio MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "audio/wav"
		}

		if len(data) > 0 {
			return genai.NewPartFromBytes(data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: audio part must provide Data, URL, or Path: %w", llm.ErrInvalidPart)

	case llm.PartTypeVideo:
		mimeType := p.MIMEType
		data := p.Data
		if len(data) == 0 && p.Path != "" {
			var err error
			data, err = os.ReadFile(p.Path)
			if err != nil {
				return nil, fmt.Errorf("google: failed to read video from %q: %w", p.Path, err)
			}
			if mimeType == "" {
				mimeType = detectMIMEType(p.Path, data)
			}
		}

		if mimeType != "" && !isSupportedVideoMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported video MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "video/mp4"
		}

		if len(data) > 0 {
			return genai.NewPartFromBytes(data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: video part must provide Data, URL, or Path: %w", llm.ErrInvalidPart)

	case llm.PartTypeDocument, llm.PartTypeFile:
		mimeType := p.MIMEType
		data := p.Data
		if len(data) == 0 && p.Path != "" {
			var err error
			data, err = os.ReadFile(p.Path)
			if err != nil {
				return nil, fmt.Errorf("google: failed to read document from %q: %w", p.Path, err)
			}
			if mimeType == "" {
				mimeType = detectMIMEType(p.Path, data)
			}
		}

		if mimeType != "" && !isSupportedDocMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported document MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "application/pdf"
		}

		if len(data) > 0 {
			return genai.NewPartFromBytes(data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: document part must provide Data, URL, or Path: %w", llm.ErrInvalidPart)

	default:
		return nil, fmt.Errorf("google: unsupported part type %q: %w", p.Type, llm.ErrUnsupportedPartType)
	}
}

// convertToolPartToGemini translates a tool media part to a Google Gen AI FunctionResponsePart.
func convertToolPartToGemini(p llm.Part) (*genai.FunctionResponsePart, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	mimeType := p.MIMEType
	data := p.Data
	if len(data) == 0 && p.Path != "" {
		var err error
		data, err = os.ReadFile(p.Path)
		if err != nil {
			return nil, fmt.Errorf("google: failed to read tool artifact from %q: %w", p.Path, err)
		}
		if mimeType == "" {
			mimeType = detectMIMEType(p.Path, data)
		}
	}

	if mimeType == "" {
		switch p.Type {
		case llm.PartTypeImage:
			mimeType = "image/png"
		case llm.PartTypeDocument, llm.PartTypeFile:
			mimeType = "application/pdf"
		case llm.PartTypeAudio:
			mimeType = "audio/wav"
		case llm.PartTypeVideo:
			mimeType = "video/mp4"
		default:
			mimeType = "application/octet-stream"
		}
	}

	if len(data) > 0 {
		return genai.NewFunctionResponsePartFromBytes(data, mimeType), nil
	}
	if p.URL != "" {
		return genai.NewFunctionResponsePartFromURI(p.URL, mimeType), nil
	}
	return nil, fmt.Errorf("google: tool part must provide Data, URL, or Path: %w", llm.ErrInvalidPart)
}

func isSupportedImageMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic", "image/heif":
		return true
	default:
		return false
	}
}

func isSupportedAudioMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "audio/wav", "audio/mp3", "audio/mpeg", "audio/aac", "audio/flac", "audio/ogg", "audio/aiff", "audio/x-m4a":
		return true
	default:
		return false
	}
}

func isSupportedVideoMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "video/mp4", "video/mpeg", "video/mov", "video/quicktime", "video/avi", "video/x-flv", "video/webm", "video/wmv", "video/3gpp":
		return true
	default:
		return false
	}
}

func isSupportedDocMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "application/pdf", "text/plain", "text/html", "text/css", "text/csv", "text/markdown", "application/rtf", "text/xml", "application/json":
		return true
	default:
		return false
	}
}

func detectMIMEType(path string, data []byte) string {
	ext := filepath.Ext(path)
	if ext != "" {
		if m := mime.TypeByExtension(ext); m != "" {
			return m
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

// convertConfig prepares the genai.GenerateContentConfig from Gojent request parameters.
func (m *Model) convertConfig(req llm.GenerateRequest, systemInstruction *genai.Content) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}

	thinkingCfg := m.resolveThinking(req)
	if thinkingCfg != nil {
		var gCfg genai.ThinkingConfig
		includeThoughts := true
		if thinkingCfg.IncludeThoughts != nil {
			includeThoughts = *thinkingCfg.IncludeThoughts
		}
		gCfg.IncludeThoughts = includeThoughts

		hasLevel := false
		if thinkingCfg.Level != "" {
			switch strings.ToUpper(string(thinkingCfg.Level)) {
			case "NONE":
				zero := int32(0)
				gCfg.ThinkingBudget = &zero
				hasLevel = true
			case "MINIMAL":
				gCfg.ThinkingLevel = genai.ThinkingLevelMinimal
				hasLevel = true
			case "LOW":
				gCfg.ThinkingLevel = genai.ThinkingLevelLow
				hasLevel = true
			case "MEDIUM":
				gCfg.ThinkingLevel = genai.ThinkingLevelMedium
				hasLevel = true
			case "HIGH":
				gCfg.ThinkingLevel = genai.ThinkingLevelHigh
				hasLevel = true
			}
		}

		// Only apply BudgetTokens if ThinkingLevel is not set, to avoid the Gemini 4/GenAI
		// 400 INVALID_ARGUMENT (thinking_budget and thinking_level are not supported together) error.
		if !hasLevel && thinkingCfg.BudgetTokens != nil {
			b := int32(*thinkingCfg.BudgetTokens)
			gCfg.ThinkingBudget = &b
		}

		cfg.ThinkingConfig = &gCfg
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

	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case llm.ResponseFormatJSON:
			cfg.ResponseMIMEType = "application/json"
		case llm.ResponseFormatJSONSchema:
			cfg.ResponseMIMEType = "application/json"
			if req.ResponseFormat.Schema != nil {
				cfg.ResponseJsonSchema = req.ResponseFormat.Schema
			}
		case llm.ResponseFormatText:
			cfg.ResponseMIMEType = "text/plain"
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

// mapGenaiLevelToLLM maps genai.ThinkingLevel to llm.ThinkingLevel.
func mapGenaiLevelToLLM(l genai.ThinkingLevel) llm.ThinkingLevel {
	switch l {
	case genai.ThinkingLevelMinimal:
		return llm.ThinkingLevelMinimal
	case genai.ThinkingLevelLow:
		return llm.ThinkingLevelLow
	case genai.ThinkingLevelMedium:
		return llm.ThinkingLevelMedium
	case genai.ThinkingLevelHigh:
		return llm.ThinkingLevelHigh
	default:
		return llm.ThinkingLevel(l)
	}
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

// extractCandidateTextAndThinking retrieves output text and thought process across candidates separately.
func extractCandidateTextAndThinking(candidates []*genai.Candidate) (text string, thinking string) {
	var textSb strings.Builder
	var thinkSb strings.Builder
	for _, cand := range candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Thought {
					thinkSb.WriteString(part.Text)
				} else if part.Text != "" {
					textSb.WriteString(part.Text)
				}
			}
		}
	}
	return textSb.String(), thinkSb.String()
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
