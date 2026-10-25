package google

import (
	"fmt"
	"math"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// convertConfig prepares the genai.GenerateContentConfig from Gojent request
// parameters. It rejects capabilities the adapter cannot honor instead of
// silently dropping them.
func (m *Model) convertConfig(req *llm.Request, systemInstruction *genai.Content) (*genai.GenerateContentConfig, error) {
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.State.Validate(); err != nil {
		return nil, err
	}
	if req.State.EffectiveMode() != llm.StateModeReplay {
		return nil, fmt.Errorf("%w: google generateContent requires replay state", llm.ErrUnsupported)
	}
	if req.State.PreviousID != "" || req.State.Store != nil {
		return nil, fmt.Errorf("%w: google generateContent does not support previous ids or stored responses", llm.ErrUnsupported)
	}

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}

	reasoning := m.resolveReasoning(req)
	if reasoning != nil {
		gCfg := &genai.ThinkingConfig{IncludeThoughts: true}
		if reasoning.IncludeThoughts != nil {
			gCfg.IncludeThoughts = *reasoning.IncludeThoughts
		}

		hasLevel := false
		switch reasoning.Level {
		case "":
		case llm.ReasoningLevelNone:
			zero := int32(0)
			gCfg.ThinkingBudget = &zero
			hasLevel = true
		case llm.ReasoningLevelMinimal:
			gCfg.ThinkingLevel = genai.ThinkingLevelMinimal
			hasLevel = true
		case llm.ReasoningLevelLow:
			gCfg.ThinkingLevel = genai.ThinkingLevelLow
			hasLevel = true
		case llm.ReasoningLevelMedium:
			gCfg.ThinkingLevel = genai.ThinkingLevelMedium
			hasLevel = true
		case llm.ReasoningLevelHigh:
			gCfg.ThinkingLevel = genai.ThinkingLevelHigh
			hasLevel = true
		default:
			return nil, fmt.Errorf("%w: reasoning level %q", llm.ErrUnsupported, reasoning.Level)
		}

		// Gemini rejects thinking_budget and thinking_level together.
		if !hasLevel && reasoning.BudgetTokens != nil {
			if *reasoning.BudgetTokens < 0 {
				return nil, fmt.Errorf("%w: negative reasoning budget %d", llm.ErrUnsupported, *reasoning.BudgetTokens)
			}
			budget := int32(*reasoning.BudgetTokens)
			gCfg.ThinkingBudget = &budget
		}
		cfg.ThinkingConfig = gCfg
	}

	if err := convertTools(req, cfg); err != nil {
		return nil, err
	}
	if err := convertResponseFormat(req.ResponseFormat, cfg); err != nil {
		return nil, err
	}

	if req.Temperature != nil {
		temperature := float32(*req.Temperature)
		cfg.Temperature = &temperature
	}
	if req.TopP != nil {
		topP := float32(*req.TopP)
		cfg.TopP = &topP
	}
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 || int64(*req.MaxTokens) > math.MaxInt32 {
			return nil, fmt.Errorf("%w: max tokens %d", llm.ErrUnsupported, *req.MaxTokens)
		}
		cfg.MaxOutputTokens = int32(*req.MaxTokens)
	}
	if len(req.Stop) > 0 {
		cfg.StopSequences = append([]string(nil), req.Stop...)
	}

	return cfg, nil
}

func convertTools(req *llm.Request, cfg *genai.GenerateContentConfig) error {
	if len(req.Tools) == 0 {
		// Auto and none are no-ops without tools; requiring a call is not.
		if req.ToolChoice.Mode == llm.ToolChoiceRequired || req.ToolChoice.Name != "" {
			return fmt.Errorf("%w: tool choice without tools", llm.ErrUnsupported)
		}
		return nil
	}

	declarations := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
	for _, definition := range req.Tools {
		if definition.EffectiveKind() != llm.ToolKindFunction {
			return fmt.Errorf("%w: google does not support %q tools", llm.ErrUnsupported, definition.EffectiveKind())
		}
		if definition.Name == "" {
			return fmt.Errorf("%w: tool definition has no name", llm.ErrInvalidToolCall)
		}
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:                 definition.Name,
			Description:          definition.Description,
			ParametersJsonSchema: definition.Parameters,
		})
	}
	cfg.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}

	if req.ToolChoice.Mode == "" && req.ToolChoice.Name == "" {
		return nil
	}
	config := &genai.FunctionCallingConfig{}
	switch req.ToolChoice.Mode {
	case "", llm.ToolChoiceAuto:
		config.Mode = genai.FunctionCallingConfigModeAuto
	case llm.ToolChoiceNone:
		config.Mode = genai.FunctionCallingConfigModeNone
	case llm.ToolChoiceRequired:
		config.Mode = genai.FunctionCallingConfigModeAny
	default:
		return fmt.Errorf("%w: tool choice mode %q", llm.ErrUnsupported, req.ToolChoice.Mode)
	}
	if req.ToolChoice.Name != "" {
		config.Mode = genai.FunctionCallingConfigModeAny
		config.AllowedFunctionNames = []string{req.ToolChoice.Name}
	}
	cfg.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: config}
	return nil
}

func convertResponseFormat(format *llm.ResponseFormat, cfg *genai.GenerateContentConfig) error {
	if format == nil {
		return nil
	}
	switch format.Type {
	case llm.ResponseFormatText:
		cfg.ResponseMIMEType = "text/plain"
	case llm.ResponseFormatJSON:
		cfg.ResponseMIMEType = "application/json"
	case llm.ResponseFormatJSONSchema:
		if len(format.Schema) == 0 {
			return fmt.Errorf("%w: json_schema response format without a schema", llm.ErrUnsupported)
		}
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseJsonSchema = format.Schema
	default:
		return fmt.Errorf("%w: response format %q", llm.ErrUnsupported, format.Type)
	}
	return nil
}

// mapGenaiLevelToLLM maps Gemini's thinking levels to canonical reasoning
// levels.
func mapGenaiLevelToLLM(level genai.ThinkingLevel) llm.ReasoningLevel {
	switch level {
	case genai.ThinkingLevelUnspecified:
		return ""
	case genai.ThinkingLevelMinimal:
		return llm.ReasoningLevelMinimal
	case genai.ThinkingLevelLow:
		return llm.ReasoningLevelLow
	case genai.ThinkingLevelMedium:
		return llm.ReasoningLevelMedium
	case genai.ThinkingLevelHigh:
		return llm.ReasoningLevelHigh
	default:
		return llm.ReasoningLevel(level)
	}
}

// mapFinishReason maps Gemini finish reasons to canonical reasons.
func mapFinishReason(reason genai.FinishReason) llm.FinishReason {
	switch reason {
	case genai.FinishReasonUnspecified:
		return ""
	case genai.FinishReasonStop:
		return llm.FinishReasonStop
	case genai.FinishReasonMaxTokens:
		return llm.FinishReasonLength
	case genai.FinishReasonSafety,
		genai.FinishReasonRecitation,
		genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonSPII,
		genai.FinishReasonImageSafety,
		genai.FinishReasonImageProhibitedContent,
		genai.FinishReasonImageRecitation:
		return llm.FinishReasonContentFilter
	case genai.FinishReasonUnexpectedToolCall, genai.FinishReasonTooManyToolCalls:
		return llm.FinishReasonTool
	default:
		return llm.FinishReason(strings.ToLower(string(reason)))
	}
}
