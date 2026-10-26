package openai

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/openai/openai-go/shared/constant"
)

// requestParams builds one Responses API request. Every feature the adapter
// cannot honor fails with llm.ErrUnsupported instead of being dropped.
func (m *Model) requestParams(req *llm.Request) (responses.ResponseNewParams, error) {
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.Validate(); err != nil {
		return responses.ResponseNewParams{}, err
	}
	if err := validateState(req.State); err != nil {
		return responses.ResponseNewParams{}, err
	}
	if raw, ok := req.ProviderOptions.Raw(providerName); ok && len(raw) > 0 {
		return responses.ResponseNewParams{}, fmt.Errorf("%w: openai provider options", llm.ErrUnsupported)
	}

	instructions, err := convertInstructions(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	items, err := convertInput(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	params := responses.ResponseNewParams{
		Model: m.modelID,
	}
	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}
	if len(items) > 0 {
		params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: items}
	}

	if err := applyState(req.State, &params); err != nil {
		return responses.ResponseNewParams{}, err
	}

	reasoning, reasoningConfigured, err := m.reasoningParam(req)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	if reasoningConfigured {
		params.Reasoning = reasoning
	}
	if reasoningConfigured || hasReasoningPart(req.Messages) {
		params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}

	text, err := convertResponseFormat(req.ResponseFormat)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params.Text = text

	if err := convertTools(req, &params); err != nil {
		return responses.ResponseNewParams{}, err
	}

	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 {
			return responses.ResponseNewParams{}, fmt.Errorf("%w: max tokens %d", llm.ErrUnsupported, *req.MaxTokens)
		}
		params.MaxOutputTokens = param.NewOpt(int64(*req.MaxTokens))
	}
	if len(req.Stop) > 0 {
		return responses.ResponseNewParams{}, fmt.Errorf("%w: openai responses does not support stop sequences", llm.ErrUnsupported)
	}

	return params, nil
}

// validateState checks the conversation state mode and rejects adapter-specific
// state options that are not implemented.
func validateState(state llm.State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if raw, ok := state.Provider.Raw(providerName); ok && len(raw) > 0 {
		return fmt.Errorf("%w: openai state options", llm.ErrUnsupported)
	}
	if state.EffectiveMode() == llm.StateModeReplay && state.PreviousID != "" {
		return fmt.Errorf("%w: previous response id requires server state", llm.ErrUnsupported)
	}
	return nil
}

// applyState maps canonical state onto previous_response_id and store.
func applyState(state llm.State, params *responses.ResponseNewParams) error {
	if state.EffectiveMode() == llm.StateModeServer {
		if state.PreviousID != "" {
			params.PreviousResponseID = param.NewOpt(state.PreviousID)
		}
		if state.Store != nil {
			params.Store = param.NewOpt(*state.Store)
		} else {
			// Server state is only useful when the provider keeps the response.
			params.Store = param.NewOpt(true)
		}
		return nil
	}
	if state.Store != nil {
		params.Store = param.NewOpt(*state.Store)
	}
	return nil
}

// convertInstructions hoists system and developer messages into the top-level
// instructions string. Instructions are re-sent on every request, including
// server-state continuations, because the provider does not carry them over.
func convertInstructions(messages []llm.Message) (string, error) {
	var blocks []string
	for _, message := range messages {
		if message.Role != llm.RoleSystem && message.Role != llm.RoleDeveloper {
			continue
		}
		if message.Name != "" || message.Phase != "" {
			return "", fmt.Errorf("%w: openai cannot represent message name or phase", llm.ErrUnsupported)
		}
		var texts []string
		for _, part := range message.Content {
			text, ok := part.(*llm.TextPart)
			if !ok {
				return "", fmt.Errorf("%w: %s part in %s message", llm.ErrUnsupportedPart, part.PartType(), message.Role)
			}
			texts = append(texts, text.Text)
		}
		if len(texts) > 0 {
			blocks = append(blocks, joinLines(texts))
		}
	}
	return joinBlocks(blocks), nil
}

// reasoningParam maps canonical reasoning settings onto Responses reasoning.
//
// The pinned API exposes low/medium/high effort. Minimal maps to low because
// the API has no distinct minimal level at this version. Explicit token
// budgets and a "none" level are not representable and fail instead of being
// silently dropped.
func (m *Model) reasoningParam(req *llm.Request) (shared.ReasoningParam, bool, error) {
	config := m.resolveReasoning(req)
	if config == nil {
		return shared.ReasoningParam{}, false, nil
	}
	if config.BudgetTokens != nil {
		return shared.ReasoningParam{}, false, fmt.Errorf(
			"%w: openai responses does not support a reasoning token budget", llm.ErrUnsupported)
	}

	var reasoning shared.ReasoningParam
	configured := false
	switch config.Level {
	case "":
	case llm.ReasoningLevelNone:
		return shared.ReasoningParam{}, false, fmt.Errorf(
			"%w: openai responses cannot disable reasoning", llm.ErrUnsupported)
	case llm.ReasoningLevelMinimal, llm.ReasoningLevelLow:
		reasoning.Effort = shared.ReasoningEffortLow
		configured = true
	case llm.ReasoningLevelMedium:
		reasoning.Effort = shared.ReasoningEffortMedium
		configured = true
	case llm.ReasoningLevelHigh:
		reasoning.Effort = shared.ReasoningEffortHigh
		configured = true
	default:
		return shared.ReasoningParam{}, false, fmt.Errorf(
			"%w: reasoning level %q", llm.ErrUnsupported, config.Level)
	}
	if config.IncludeThoughts != nil && *config.IncludeThoughts {
		reasoning.Summary = shared.ReasoningSummaryAuto
		configured = true
	}
	return reasoning, configured, nil
}

// convertResponseFormat maps canonical structured output onto Responses text
// configuration.
func convertResponseFormat(format *llm.ResponseFormat) (responses.ResponseTextConfigParam, error) {
	if format == nil {
		return responses.ResponseTextConfigParam{}, nil
	}
	switch format.Type {
	case "", llm.ResponseFormatText:
		return responses.ResponseTextConfigParam{}, nil
	case llm.ResponseFormatJSON:
		return responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: constant.ValueOf[constant.JSONObject]()},
			},
		}, nil
	case llm.ResponseFormatJSONSchema:
		if len(format.Schema) == 0 {
			return responses.ResponseTextConfigParam{}, fmt.Errorf(
				"%w: json schema response format has no schema", llm.ErrUnsupported)
		}
		name := format.Name
		if name == "" {
			name = "output"
		}
		schema := &responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   name,
			Schema: format.Schema,
			Type:   constant.ValueOf[constant.JSONSchema](),
		}
		if format.Strict {
			schema.Strict = param.NewOpt(true)
		}
		return responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{OfJSONSchema: schema},
		}, nil
	default:
		return responses.ResponseTextConfigParam{}, fmt.Errorf(
			"%w: response format %q", llm.ErrUnsupported, format.Type)
	}
}

// convertTools maps canonical tool definitions and choice onto Responses
// tools. Function and client-executed MCP tools become function tools. Server
// tools carry their OpenAI definition in provider metadata.
func convertTools(req *llm.Request, params *responses.ResponseNewParams) error {
	if len(req.Tools) == 0 {
		if req.ToolChoice.Mode == llm.ToolChoiceRequired || req.ToolChoice.Name != "" {
			return fmt.Errorf("%w: tool choice without tools", llm.ErrUnsupported)
		}
		return nil
	}

	tools := make([]responses.ToolUnionParam, 0, len(req.Tools))
	names := make(map[string]llm.ToolKind, len(req.Tools))
	for _, definition := range req.Tools {
		if definition.Name == "" {
			return fmt.Errorf("%w: tool definition has no name", llm.ErrInvalidToolCall)
		}
		if _, duplicate := names[definition.Name]; duplicate {
			return fmt.Errorf("%w: duplicate tool %q", llm.ErrInvalidToolCall, definition.Name)
		}
		names[definition.Name] = definition.EffectiveKind()

		switch definition.EffectiveKind() {
		case llm.ToolKindFunction, llm.ToolKindMCP:
			tool, err := functionTool(definition)
			if err != nil {
				return err
			}
			tools = append(tools, responses.ToolUnionParam{OfFunction: tool})
		case llm.ToolKindServer:
			tool, err := hostedTool(definition)
			if err != nil {
				return err
			}
			tools = append(tools, tool)
		default:
			return fmt.Errorf("%w: openai does not support %q tools", llm.ErrUnsupported, definition.EffectiveKind())
		}
	}
	params.Tools = tools

	choice, err := convertToolChoice(req.ToolChoice, names)
	if err != nil {
		return err
	}
	params.ToolChoice = choice
	return nil
}

func functionTool(definition llm.ToolDefinition) (*responses.FunctionToolParam, error) {
	schema := definition.Parameters
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	if rawType, ok := schema["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok || typeName != "object" {
			return nil, fmt.Errorf("%w: openai tool schemas must have type object", llm.ErrUnsupported)
		}
	} else {
		schema = cloneSchema(schema)
		schema["type"] = "object"
	}
	tool := &responses.FunctionToolParam{
		Name:       definition.Name,
		Parameters: schema,
		Type:       constant.ValueOf[constant.Function](),
	}
	if definition.Description != "" {
		tool.Description = param.NewOpt(definition.Description)
	}
	if definition.Strict {
		tool.Strict = param.NewOpt(true)
	}
	return tool, nil
}

// hostedTool decodes a hosted tool definition from provider metadata. The
// provider value is the raw OpenAI tool object, for example
// {"type":"web_search_preview"}.
func hostedTool(definition llm.ToolDefinition) (responses.ToolUnionParam, error) {
	raw, ok := definition.Provider.Raw(providerName)
	if !ok || len(raw) == 0 {
		return responses.ToolUnionParam{}, fmt.Errorf(
			"%w: server tool %q needs openai provider configuration", llm.ErrUnsupported, definition.Name)
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
	}

	switch probe.Type {
	case "web_search_preview", "web_search_preview_2025_03_11":
		var tool responses.WebSearchToolParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfWebSearchPreview: &tool}, nil
	case "file_search":
		var tool responses.FileSearchToolParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfFileSearch: &tool}, nil
	case "computer_use_preview":
		var tool responses.ComputerToolParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfComputerUsePreview: &tool}, nil
	case "mcp":
		var tool responses.ToolMcpParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfMcp: &tool}, nil
	case "code_interpreter":
		var tool responses.ToolCodeInterpreterParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfCodeInterpreter: &tool}, nil
	case "image_generation":
		var tool responses.ToolImageGenerationParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfImageGeneration: &tool}, nil
	case "local_shell":
		var tool responses.ToolLocalShellParam
		if err := json.Unmarshal(raw, &tool); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("openai: decode tool %q: %w", definition.Name, err)
		}
		return responses.ToolUnionParam{OfLocalShell: &tool}, nil
	default:
		return responses.ToolUnionParam{}, fmt.Errorf(
			"%w: hosted tool type %q", llm.ErrUnsupported, probe.Type)
	}
}

func convertToolChoice(choice llm.ToolChoice, names map[string]llm.ToolKind) (responses.ResponseNewParamsToolChoiceUnion, error) {
	if choice.Name != "" {
		if choice.Mode != "" && choice.Mode != llm.ToolChoiceAuto {
			return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf(
				"%w: tool choice name with mode %q", llm.ErrUnsupported, choice.Mode)
		}
		if kind, ok := names[choice.Name]; !ok || kind == llm.ToolKindServer {
			return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf(
				"%w: tool choice %q must name a function tool", llm.ErrUnsupported, choice.Name)
		}
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{
				Name: choice.Name,
				Type: constant.ValueOf[constant.Function](),
			},
		}, nil
	}

	switch choice.Mode {
	case "":
		return responses.ResponseNewParamsToolChoiceUnion{}, nil
	case llm.ToolChoiceAuto:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		}, nil
	case llm.ToolChoiceNone:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
		}, nil
	case llm.ToolChoiceRequired:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsRequired),
		}, nil
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf(
			"%w: tool choice mode %q", llm.ErrUnsupported, choice.Mode)
	}
}

func hasReasoningPart(messages []llm.Message) bool {
	for _, message := range messages {
		for _, part := range message.Content {
			if _, ok := part.(*llm.ReasoningPart); ok {
				return true
			}
		}
	}
	return false
}

func cloneSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		out[key] = value
	}
	return out
}

func joinLines(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += "\n" + value
	}
	return out
}

func joinBlocks(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += "\n\n" + value
	}
	return out
}
