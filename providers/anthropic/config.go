package anthropic

import (
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/ijaidev/gojent/core/llm"
)

const (
	// minThinkingBudget is the smallest budget the Messages API accepts.
	minThinkingBudget = 1024
	// defaultThinkingBudget is used when reasoning is requested without a
	// level or an explicit budget.
	defaultThinkingBudget = 4096
)

// thinkingBudgets maps qualitative levels to token budgets. Levels are
// approximate by nature; explicit budgets always win.
var thinkingBudgets = map[llm.ReasoningLevel]int{
	llm.ReasoningLevelMinimal: 1024,
	llm.ReasoningLevelLow:     2048,
	llm.ReasoningLevelMedium:  4096,
	llm.ReasoningLevelHigh:    8192,
}

// requestParams builds one Messages API request. Every feature the adapter
// cannot honor fails with llm.ErrUnsupported instead of being dropped.
func (m *Model) requestParams(req *llm.Request) (anthropic.MessageNewParams, error) {
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.Validate(); err != nil {
		return anthropic.MessageNewParams{}, err
	}
	if err := validateState(req.State); err != nil {
		return anthropic.MessageNewParams{}, err
	}

	messages, system, err := convertMessages(req.Messages)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}

	maxTokens, err := m.resolveMaxTokens(req.MaxTokens)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}

	params := anthropic.MessageNewParams{
		Model:     m.modelID,
		MaxTokens: maxTokens,
		Messages:  messages,
		System:    system,
	}

	thinking, thinkingEnabled, err := m.thinkingConfig(req, maxTokens)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}
	params.Thinking = thinking

	if err := checkThinkingSampling(req, thinkingEnabled); err != nil {
		return anthropic.MessageNewParams{}, err
	}
	if err := convertResponseFormat(req.ResponseFormat); err != nil {
		return anthropic.MessageNewParams{}, err
	}
	if err := convertTools(req, &params); err != nil {
		return anthropic.MessageNewParams{}, err
	}

	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = param.NewOpt(*req.TopP)
	}
	if len(req.Stop) > 0 {
		params.StopSequences = append([]string(nil), req.Stop...)
	}

	return params, nil
}

// validateState rejects server-side state. The Messages API has no stored
// conversations or previous-response references.
func validateState(state llm.State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if state.EffectiveMode() != llm.StateModeReplay {
		return fmt.Errorf("%w: anthropic requires replay state", llm.ErrUnsupported)
	}
	if state.PreviousID != "" || state.Store != nil {
		return fmt.Errorf("%w: anthropic does not support previous ids or stored responses", llm.ErrUnsupported)
	}
	if len(state.Provider) > 0 {
		return fmt.Errorf("%w: anthropic state options", llm.ErrUnsupported)
	}
	return nil
}

// resolveMaxTokens applies the adapter default. The API requires max_tokens on
// every request.
func (m *Model) resolveMaxTokens(requested *int) (int64, error) {
	if requested == nil {
		return int64(m.maxTokens), nil
	}
	if *requested <= 0 {
		return 0, fmt.Errorf("%w: max tokens %d", llm.ErrUnsupported, *requested)
	}
	return int64(*requested), nil
}

// thinkingConfig maps canonical reasoning settings onto extended thinking.
//
// The API only accepts an explicit token budget, so qualitative levels are
// mapped to fixed budgets. Disabled is explicit; absent reasoning leaves
// thinking off.
func (m *Model) thinkingConfig(req *llm.Request, maxTokens int64) (anthropic.ThinkingConfigParamUnion, bool, error) {
	config := m.resolveReasoning(req)
	if config == nil {
		return anthropic.ThinkingConfigParamUnion{}, false, nil
	}
	if config.Level == llm.ReasoningLevelNone {
		if config.BudgetTokens != nil {
			return anthropic.ThinkingConfigParamUnion{}, false, fmt.Errorf(
				"%w: reasoning level none cannot carry a thinking budget", llm.ErrUnsupported)
		}
		return anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
		}, false, nil
	}

	budget := 0
	if config.BudgetTokens != nil {
		budget = *config.BudgetTokens
		if budget < minThinkingBudget {
			return anthropic.ThinkingConfigParamUnion{}, false, fmt.Errorf(
				"%w: thinking budget %d is below the minimum of %d", llm.ErrUnsupported, budget, minThinkingBudget)
		}
		if int64(budget) >= maxTokens {
			return anthropic.ThinkingConfigParamUnion{}, false, fmt.Errorf(
				"%w: thinking budget %d must be less than max tokens %d", llm.ErrUnsupported, budget, maxTokens)
		}
	} else {
		switch config.Level {
		case "":
			if config.IncludeThoughts == nil || !*config.IncludeThoughts {
				return anthropic.ThinkingConfigParamUnion{}, false, nil
			}
		case llm.ReasoningLevelMinimal, llm.ReasoningLevelLow,
			llm.ReasoningLevelMedium, llm.ReasoningLevelHigh:
			budget = thinkingBudgets[config.Level]
		default:
			return anthropic.ThinkingConfigParamUnion{}, false, fmt.Errorf(
				"%w: reasoning level %q", llm.ErrUnsupported, config.Level)
		}
		if budget == 0 {
			budget = defaultThinkingBudget
		}
		if int64(budget) >= maxTokens {
			budget = int(maxTokens) - 1
		}
		if budget < minThinkingBudget {
			return anthropic.ThinkingConfigParamUnion{}, false, fmt.Errorf(
				"%w: max tokens %d cannot hold the minimum thinking budget", llm.ErrUnsupported, maxTokens)
		}
	}

	enabled := &anthropic.ThinkingConfigEnabledParam{BudgetTokens: int64(budget)}
	if config.IncludeThoughts != nil && !*config.IncludeThoughts {
		enabled.Display = anthropic.ThinkingConfigEnabledDisplayOmitted
	}
	return anthropic.ThinkingConfigParamUnion{OfEnabled: enabled}, true, nil
}

// checkThinkingSampling enforces the API's sampling constraints while thinking
// is enabled: temperature must be 1 and top_p is not accepted.
func checkThinkingSampling(req *llm.Request, enabled bool) error {
	if !enabled {
		return nil
	}
	if req.Temperature != nil && *req.Temperature != 1 {
		return fmt.Errorf("%w: extended thinking requires temperature 1, got %v", llm.ErrUnsupported, *req.Temperature)
	}
	if req.TopP != nil {
		return fmt.Errorf("%w: extended thinking does not accept top_p", llm.ErrUnsupported)
	}
	return nil
}

// convertResponseFormat rejects output formats the Messages API does not
// support. Plain text is the API default and therefore a no-op.
func convertResponseFormat(format *llm.ResponseFormat) error {
	if format == nil || format.Type == llm.ResponseFormatText {
		return nil
	}
	return fmt.Errorf("%w: response format %q", llm.ErrUnsupported, format.Type)
}

// convertTools adds function tools and maps tool choice. Hosted tools are not
// advertised: their results are recorded when replaying a transcript, but the
// adapter never asks the API to run one.
func convertTools(req *llm.Request, params *anthropic.MessageNewParams) error {
	if len(req.Tools) == 0 {
		if req.ToolChoice.Mode == llm.ToolChoiceRequired || req.ToolChoice.Name != "" {
			return fmt.Errorf("%w: tool choice without tools", llm.ErrUnsupported)
		}
		return nil
	}

	tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
	names := make(map[string]struct{}, len(req.Tools))
	for _, definition := range req.Tools {
		if definition.EffectiveKind() != llm.ToolKindFunction {
			return fmt.Errorf("%w: anthropic does not support %q tools", llm.ErrUnsupported, definition.EffectiveKind())
		}
		if definition.Name == "" {
			return fmt.Errorf("%w: tool definition has no name", llm.ErrInvalidToolCall)
		}
		if _, duplicate := names[definition.Name]; duplicate {
			return fmt.Errorf("%w: duplicate tool %q", llm.ErrInvalidToolCall, definition.Name)
		}
		names[definition.Name] = struct{}{}

		schema, err := toolInputSchema(definition.Parameters)
		if err != nil {
			return err
		}
		tool := &anthropic.ToolParam{
			Name:        definition.Name,
			InputSchema: schema,
		}
		if definition.Description != "" {
			tool.Description = param.NewOpt(definition.Description)
		}
		if definition.Strict {
			tool.Strict = param.NewOpt(true)
		}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: tool})
	}
	params.Tools = tools

	if req.ToolChoice.Mode == "" && req.ToolChoice.Name == "" {
		return nil
	}
	choice, err := convertToolChoice(req.ToolChoice)
	if err != nil {
		return err
	}
	params.ToolChoice = choice
	return nil
}

// toolInputSchema preserves every schema keyword. Anthropic requires an object
// schema; properties, required, and the remaining keywords are copied through.
func toolInputSchema(parameters map[string]any) (anthropic.ToolInputSchemaParam, error) {
	schema := anthropic.ToolInputSchemaParam{Type: "object"}
	if len(parameters) == 0 {
		return schema, nil
	}

	extras := make(map[string]any, len(parameters))
	for key, value := range parameters {
		extras[key] = value
	}
	if rawType, ok := extras["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok || typeName != "object" {
			return anthropic.ToolInputSchemaParam{}, fmt.Errorf(
				"%w: anthropic tool schemas must have type object", llm.ErrUnsupported)
		}
		delete(extras, "type")
	}
	if properties, ok := extras["properties"]; ok {
		schema.Properties = properties
		delete(extras, "properties")
	}
	if rawRequired, ok := extras["required"]; ok {
		required, err := schemaStrings(rawRequired)
		if err != nil {
			return anthropic.ToolInputSchemaParam{}, err
		}
		schema.Required = required
		delete(extras, "required")
	}
	if len(extras) > 0 {
		schema.ExtraFields = extras
	}
	return schema, nil
}

// schemaStrings accepts both []string and the []any produced by JSON decoding.
func schemaStrings(value any) ([]string, error) {
	switch list := value.(type) {
	case []string:
		return append([]string(nil), list...), nil
	case []any:
		out := make([]string, 0, len(list))
		for index, item := range list {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: required[%d] is %T", llm.ErrUnsupported, index, item)
			}
			out = append(out, name)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: required is %T", llm.ErrUnsupported, value)
	}
}

// convertToolChoice maps canonical tool choice onto the Messages API unions.
func convertToolChoice(choice llm.ToolChoice) (anthropic.ToolChoiceUnionParam, error) {
	if choice.Name != "" {
		switch choice.Mode {
		case "", llm.ToolChoiceAuto, llm.ToolChoiceRequired:
		default:
			return anthropic.ToolChoiceUnionParam{}, fmt.Errorf(
				"%w: tool choice mode %q with a forced tool", llm.ErrUnsupported, choice.Mode)
		}
		return anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{Name: choice.Name},
		}, nil
	}

	switch choice.Mode {
	case "", llm.ToolChoiceAuto:
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}, nil
	case llm.ToolChoiceNone:
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}, nil
	case llm.ToolChoiceRequired:
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}, nil
	default:
		return anthropic.ToolChoiceUnionParam{}, fmt.Errorf("%w: tool choice mode %q", llm.ErrUnsupported, choice.Mode)
	}
}
