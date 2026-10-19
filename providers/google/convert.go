package google

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

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
