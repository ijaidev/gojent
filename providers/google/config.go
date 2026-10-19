package google

import (
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

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
