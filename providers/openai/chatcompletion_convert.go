package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
)

// convertChatMessages translates the canonical transcript into Chat
// Completions messages.
//
// System and developer instructions are hoisted into one leading system
// message for compatibility with servers that accept only a single system
// message. Tool results travel as tool-role messages; remaining user content
// follows them. Reasoning parts have no Chat Completions representation and are
// skipped on replay.
func convertChatMessages(messages []llm.Message) ([]sdk.ChatCompletionMessageParamUnion, error) {
	out := make([]sdk.ChatCompletionMessageParamUnion, 0, len(messages))
	var system []string

	for index := range messages {
		message := &messages[index]
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			for partIndex, part := range message.Content {
				text, ok := part.(*llm.TextPart)
				if !ok {
					return nil, fmt.Errorf("%w: system content[%d] is %T", llm.ErrUnsupportedPart, partIndex, part)
				}
				system = append(system, text.Text)
			}
		case llm.RoleUser:
			converted, err := convertChatUserMessage(message)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", index, err)
			}
			out = append(out, converted...)
		case llm.RoleAssistant:
			converted, err := convertChatAssistantMessage(message)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", index, err)
			}
			out = append(out, converted)
		default:
			return nil, fmt.Errorf("%w: role %q", llm.ErrInvalidMessage, message.Role)
		}
	}

	if len(system) > 0 {
		out = append([]sdk.ChatCompletionMessageParamUnion{
			sdk.SystemMessage(strings.Join(system, "\n\n")),
		}, out...)
	}
	return out, nil
}

func convertChatUserMessage(message *llm.Message) ([]sdk.ChatCompletionMessageParamUnion, error) {
	var (
		out      []sdk.ChatCompletionMessageParamUnion
		content  []sdk.ChatCompletionContentPartUnionParam
		text     []string
		hasImage bool
	)

	for partIndex, part := range message.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			text = append(text, p.Text)
			content = append(content, sdk.TextContentPart(p.Text))
		case *llm.FilePart:
			image, err := chatImagePart(p)
			if err != nil {
				return nil, fmt.Errorf("content[%d]: %w", partIndex, err)
			}
			content = append(content, image)
			hasImage = true
		case *llm.ToolResultPart:
			converted, err := convertChatToolResult(&p.ToolResult)
			if err != nil {
				return nil, fmt.Errorf("content[%d]: %w", partIndex, err)
			}
			out = append(out, converted)
		default:
			return nil, fmt.Errorf("%w: user content[%d] is %T", llm.ErrUnsupportedPart, partIndex, part)
		}
	}

	if len(content) > 0 {
		if hasImage {
			out = append(out, sdk.UserMessage(content))
		} else {
			out = append(out, sdk.UserMessage(strings.Join(text, "\n")))
		}
	}
	return out, nil
}

func convertChatAssistantMessage(message *llm.Message) (sdk.ChatCompletionMessageParamUnion, error) {
	var (
		text      []string
		refusals  []string
		toolCalls []sdk.ChatCompletionMessageToolCallParam
	)

	for partIndex, part := range message.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			text = append(text, p.Text)
		case *llm.RefusalPart:
			refusals = append(refusals, p.Text)
		case *llm.ReasoningPart:
			// Chat Completions has no reasoning input field.
			continue
		case *llm.ToolCallPart:
			switch p.EffectiveKind() {
			case llm.ToolKindFunction, llm.ToolKindMCP:
			default:
				return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
					"%w: content[%d] tool kind %q", llm.ErrUnsupported, partIndex, p.EffectiveKind())
			}
			arguments, err := chatArguments(p.Input)
			if err != nil {
				return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf("content[%d]: %w", partIndex, err)
			}
			toolCalls = append(toolCalls, sdk.ChatCompletionMessageToolCallParam{
				ID: p.CallID,
				Function: sdk.ChatCompletionMessageToolCallFunctionParam{
					Name:      p.Name,
					Arguments: string(arguments),
				},
			})
		default:
			return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
				"%w: assistant content[%d] is %T", llm.ErrUnsupportedPart, partIndex, part)
		}
	}

	if len(text) == 0 && len(refusals) == 0 && len(toolCalls) == 0 {
		return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
			"%w: assistant message has no replayable content", llm.ErrUnsupportedPart)
	}

	assistant := &sdk.ChatCompletionAssistantMessageParam{ToolCalls: toolCalls}
	if len(text) > 0 {
		assistant.Content.OfString = param.NewOpt(strings.Join(text, "\n"))
	}
	if len(refusals) > 0 {
		assistant.Refusal = param.NewOpt(strings.Join(refusals, "\n"))
	}
	return sdk.ChatCompletionMessageParamUnion{OfAssistant: assistant}, nil
}

func convertChatToolResult(result *llm.ToolResult) (sdk.ChatCompletionMessageParamUnion, error) {
	if result == nil {
		return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf("%w: tool result is nil", llm.ErrInvalidToolResult)
	}
	if result.CallID == "" {
		return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf("%w: tool result has no call id", llm.ErrInvalidToolResult)
	}
	switch result.EffectiveKind() {
	case llm.ToolKindFunction, llm.ToolKindMCP:
	default:
		return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
			"%w: Chat Completions does not support %q tool results", llm.ErrUnsupported, result.EffectiveKind())
	}

	blocks := make([]string, 0, len(result.Content)+1)
	for index, part := range result.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			blocks = append(blocks, p.Text)
		case *llm.FilePart:
			return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
				"%w: Chat Completions tool outputs cannot carry files", llm.ErrUnsupportedPartType)
		default:
			return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
				"%w: content[%d] is %T", llm.ErrInvalidToolResult, index, part)
		}
	}
	if len(result.Structured) > 0 {
		blocks = append(blocks, string(result.Structured))
	}
	return sdk.ToolMessage(strings.Join(blocks, "\n"), result.CallID), nil
}

func chatImagePart(part *llm.FilePart) (sdk.ChatCompletionContentPartUnionParam, error) {
	mimeType := normalizeOpenAIMIME(part.MIMEType)
	if !strings.HasPrefix(mimeType, "image/") {
		return sdk.ChatCompletionContentPartUnionParam{}, fmt.Errorf(
			"%w: Chat Completions cannot send %q", llm.ErrUnsupportedMIMEType, part.MIMEType)
	}
	detail := strings.ToLower(strings.TrimSpace(part.Detail))
	switch detail {
	case "", "auto", "low", "high":
	default:
		return sdk.ChatCompletionContentPartUnionParam{}, fmt.Errorf("%w: image detail %q", llm.ErrUnsupported, part.Detail)
	}

	var url string
	switch {
	case len(part.Data) > 0:
		url = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
	case part.URL != "":
		url = part.URL
	case part.FileID != "":
		return sdk.ChatCompletionContentPartUnionParam{}, fmt.Errorf(
			"%w: Chat Completions cannot reference image file IDs", llm.ErrUnsupported)
	default:
		return sdk.ChatCompletionContentPartUnionParam{}, fmt.Errorf("%w: image has no source", llm.ErrInvalidPart)
	}
	return sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{
		URL:    url,
		Detail: detail,
	}), nil
}

func convertChatTools(tools []llm.ToolDefinition) ([]sdk.ChatCompletionToolParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]sdk.ChatCompletionToolParam, 0, len(tools))
	names := make(map[string]struct{}, len(tools))
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("%w: tool %d has no name", llm.ErrInvalidToolCall, index)
		}
		if _, duplicate := names[tool.Name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate tool %q", llm.ErrInvalidToolCall, tool.Name)
		}
		names[tool.Name] = struct{}{}
		switch tool.EffectiveKind() {
		case llm.ToolKindFunction, llm.ToolKindMCP:
		default:
			return nil, fmt.Errorf("%w: tool %d kind %q", llm.ErrUnsupported, index, tool.EffectiveKind())
		}
		definition := shared.FunctionDefinitionParam{Name: tool.Name}
		if tool.Description != "" {
			definition.Description = param.NewOpt(tool.Description)
		}
		if len(tool.Parameters) > 0 {
			definition.Parameters = shared.FunctionParameters(tool.Parameters)
		}
		if tool.Strict {
			definition.Strict = param.NewOpt(true)
		}
		out = append(out, sdk.ChatCompletionToolParam{Function: definition})
	}
	return out, nil
}

func convertChatToolChoice(choice llm.ToolChoice, tools []llm.ToolDefinition) (sdk.ChatCompletionToolChoiceOptionUnionParam, error) {
	if choice.Name != "" {
		if choice.Mode != "" && choice.Mode != llm.ToolChoiceAuto {
			return sdk.ChatCompletionToolChoiceOptionUnionParam{}, fmt.Errorf(
				"%w: tool choice name with mode %q", llm.ErrUnsupported, choice.Mode)
		}
		known := false
		for _, tool := range tools {
			if tool.Name == choice.Name && tool.EffectiveKind() != llm.ToolKindServer {
				known = true
				break
			}
		}
		if !known {
			return sdk.ChatCompletionToolChoiceOptionUnionParam{}, fmt.Errorf(
				"%w: tool choice %q must name a function tool", llm.ErrUnsupported, choice.Name)
		}
		return sdk.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
			sdk.ChatCompletionNamedToolChoiceFunctionParam{Name: choice.Name},
		), nil
	}
	switch choice.Mode {
	case "":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{}, nil
	case llm.ToolChoiceAuto:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("auto")}, nil
	case llm.ToolChoiceNone:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("none")}, nil
	case llm.ToolChoiceRequired:
		if len(tools) == 0 {
			return sdk.ChatCompletionToolChoiceOptionUnionParam{}, fmt.Errorf(
				"%w: tool choice required without tools", llm.ErrUnsupported)
		}
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("required")}, nil
	default:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{}, fmt.Errorf(
			"%w: tool choice %q", llm.ErrUnsupported, choice.Mode)
	}
}

func convertChatResponseFormat(format *llm.ResponseFormat) (sdk.ChatCompletionNewParamsResponseFormatUnion, error) {
	if format == nil {
		return sdk.ChatCompletionNewParamsResponseFormatUnion{}, nil
	}
	switch format.Type {
	case "", llm.ResponseFormatText:
		return sdk.ChatCompletionNewParamsResponseFormatUnion{}, nil
	case llm.ResponseFormatJSON:
		return sdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}, nil
	case llm.ResponseFormatJSONSchema:
		if len(format.Schema) == 0 {
			return sdk.ChatCompletionNewParamsResponseFormatUnion{}, fmt.Errorf(
				"%w: JSON schema response format has no schema", llm.ErrUnsupported)
		}
		name := strings.TrimSpace(format.Name)
		if name == "" {
			name = "response"
		}
		return sdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   name,
					Schema: format.Schema,
					Strict: param.NewOpt(format.Strict),
				},
			},
		}, nil
	default:
		return sdk.ChatCompletionNewParamsResponseFormatUnion{}, fmt.Errorf(
			"%w: response format %q", llm.ErrUnsupported, format.Type)
	}
}

// chatArguments normalizes a canonical tool input into a Chat Completions
// arguments string. An absent value is an empty object.
func chatArguments(input json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return "{}", nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("%w: tool arguments are not valid JSON", llm.ErrInvalidToolCall)
	}
	return trimmed, nil
}
