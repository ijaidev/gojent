package google

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

const providerName = "google"

// providerDataFromContent serializes replay-critical Gemini content, including
// thought signatures.
func providerDataFromContent(content *genai.Content) *llm.ProviderData {
	if content == nil || len(content.Parts) == 0 {
		return nil
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	return &llm.ProviderData{Provider: providerName, Version: 1, Data: encoded}
}

// decodeProviderData restores Gemini content saved by providerDataFromContent.
func decodeProviderData(data *llm.ProviderData) *genai.Content {
	if data == nil || data.Provider != providerName || len(data.Data) == 0 {
		return nil
	}
	var content genai.Content
	if err := json.Unmarshal(data.Data, &content); err != nil {
		return nil
	}
	if len(content.Parts) == 0 {
		return nil
	}
	if content.Role == "" {
		content.Role = genai.RoleModel
	}
	return &content
}

// convertMessages translates canonical messages into Gemini contents and a
// system instruction.
func convertMessages(messages []llm.Message) (contents []*genai.Content, systemInstruction *genai.Content, err error) {
	var systemParts []*genai.Part

	for i := range messages {
		msg := &messages[i]
		switch msg.Role {
		case llm.RoleSystem:
			for _, part := range msg.Content {
				if part.Type != llm.PartTypeText {
					return nil, nil, fmt.Errorf("google: system message only supports text parts: %w", llm.ErrUnsupportedPartType)
				}
				if part.Text != "" {
					systemParts = append(systemParts, genai.NewPartFromText(part.Text))
				}
			}

		case llm.RoleAssistant:
			if raw := decodeProviderData(msg.ProviderData); raw != nil {
				contents = append(contents, raw)
				continue
			}
			var parts []*genai.Part
			for _, part := range msg.Content {
				if part.Type == llm.PartTypeText && part.Text == "" {
					continue
				}
				converted, convertErr := convertPartToGemini(part)
				if convertErr != nil {
					return nil, nil, convertErr
				}
				parts = append(parts, converted)
			}
			for _, call := range msg.ToolCalls {
				args := map[string]any{}
				if call.Arguments != "" {
					if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
						return nil, nil, fmt.Errorf("google: invalid arguments for tool %q: %w", call.Name, err)
					}
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   call.ID,
						Name: call.Name,
						Args: args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: genai.RoleModel, Parts: parts})
			}

		case llm.RoleTool:
			name := msg.Name
			if name == "" {
				name = msg.ToolCallID
			}

			var functionResponseParts []*genai.FunctionResponsePart
			var responseMap map[string]any
			for _, part := range msg.Content {
				if part.Type == llm.PartTypeText {
					if part.Text == "" {
						continue
					}
					if responseMap == nil {
						if err := json.Unmarshal([]byte(part.Text), &responseMap); err != nil {
							responseMap = map[string]any{"output": part.Text}
						}
					}
					continue
				}
				converted, convertErr := convertToolPartToGemini(part)
				if convertErr != nil {
					return nil, nil, convertErr
				}
				functionResponseParts = append(functionResponseParts, converted)
			}
			if responseMap == nil {
				responseMap = map[string]any{"output": ""}
			}
			if msg.IsError {
				responseMap["is_error"] = true
			}

			var responsePart *genai.Part
			if len(functionResponseParts) > 0 {
				responsePart = genai.NewPartFromFunctionResponseWithParts(name, responseMap, functionResponseParts)
			} else {
				responsePart = genai.NewPartFromFunctionResponse(name, responseMap)
			}
			if responsePart != nil && responsePart.FunctionResponse != nil {
				responsePart.FunctionResponse.ID = msg.ToolCallID
			}

			if len(contents) > 0 && contents[len(contents)-1].Role == genai.RoleUser &&
				len(contents[len(contents)-1].Parts) > 0 &&
				contents[len(contents)-1].Parts[0].FunctionResponse != nil {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, responsePart)
			} else {
				contents = append(contents, &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{responsePart}})
			}

		default:
			var parts []*genai.Part
			for _, part := range msg.Content {
				if part.Type == llm.PartTypeText && part.Text == "" {
					continue
				}
				converted, convertErr := convertPartToGemini(part)
				if convertErr != nil {
					return nil, nil, convertErr
				}
				parts = append(parts, converted)
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: string(msg.Role), Parts: parts})
			}
		}
	}

	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{Parts: systemParts}
	}
	return contents, systemInstruction, nil
}
