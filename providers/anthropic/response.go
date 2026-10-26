package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ijaidev/gojent/core/llm"
)

// canonicalResponse converts a non-streaming Messages response into canonical
// form. Hosted-tool result blocks are recorded on their server tool call
// instead of being exposed as separately executable content.
func (m *Model) canonicalResponse(message *anthropic.Message) (*llm.Response, error) {
	if message == nil {
		return nil, fmt.Errorf("%w: nil response", llm.ErrInvalidMessage)
	}

	parts, err := canonicalPartsFromBlocks(message.Content)
	if err != nil {
		return nil, err
	}

	rawReason := string(message.StopReason)
	finish, status := mapStopReason(message.StopReason)
	if finish == "" {
		finish = llm.FinishReasonStop
	}

	if len(parts) == 0 {
		if message.StopReason == anthropic.StopReasonRefusal {
			parts = []llm.Part{llm.NewRefusalPart(refusalText(message.StopDetails.Explanation))}
		} else {
			return nil, fmt.Errorf("anthropic: response contains no content: %w", llm.ErrInvalidMessage)
		}
	}

	canonical := llm.Message{
		Role:    llm.RoleAssistant,
		Content: parts,
		Status:  status,
	}
	if err := canonical.Validate(); err != nil {
		return nil, err
	}

	modelID := message.Model
	if modelID == "" {
		modelID = m.modelID
	}
	return &llm.Response{
		ID:              message.ID,
		Model:           modelID,
		Message:         canonical,
		Status:          status,
		FinishReason:    finish,
		RawFinishReason: rawReason,
		Usage:           usageFromMessage(message.Usage),
	}, nil
}

// canonicalPartsFromBlocks converts content blocks in order. Hosted results
// annotate the server tool call they belong to and add no canonical part.
func canonicalPartsFromBlocks(blocks []anthropic.ContentBlockUnion) ([]llm.Part, error) {
	parts := make([]llm.Part, 0, len(blocks))
	for index := range blocks {
		block := &blocks[index]
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			parts = append(parts, llm.NewTextPart(block.Text))

		case "thinking":
			if block.Signature == "" {
				return nil, fmt.Errorf("%w: thinking block has no signature", llm.ErrUnsupportedPart)
			}
			part := llm.NewReasoningPart(block.Thinking)
			if err := attachState(part, partState{Signature: block.Signature}); err != nil {
				return nil, err
			}
			parts = append(parts, part)

		case "redacted_thinking":
			if block.Data == "" {
				return nil, fmt.Errorf("%w: redacted thinking block has no data", llm.ErrUnsupportedPart)
			}
			part := llm.NewReasoningPart("")
			part.Redacted = true
			if err := attachState(part, partState{RedactedData: block.Data}); err != nil {
				return nil, err
			}
			parts = append(parts, part)

		case "tool_use":
			call := llm.ToolCall{
				CallID: block.ID,
				Name:   block.Name,
				Kind:   llm.ToolKindFunction,
				Input:  canonicalInput(block.Input),
				Status: llm.ToolStatusCompleted,
			}
			part := llm.NewToolCallPart(call)
			parts = append(parts, part)

		case "server_tool_use":
			input, err := marshalInput(block.Input)
			if err != nil {
				return nil, fmt.Errorf("anthropic: content[%d]: %w", index, err)
			}
			part := llm.NewToolCallPart(llm.ToolCall{
				CallID: block.ID,
				Name:   block.Name,
				Kind:   llm.ToolKindServer,
				Input:  input,
				Status: llm.ToolStatusCompleted,
			})
			parts = append(parts, part)

		default:
			if _, hosted := hostedResultTypes[block.Type]; hosted {
				if err := attachServerResult(parts, block.ToolUseID, block.RawJSON()); err != nil {
					return nil, fmt.Errorf("anthropic: content[%d]: %w", index, err)
				}
				continue
			}
			return nil, fmt.Errorf("%w: anthropic content block %q", llm.ErrUnsupportedPartType, block.Type)
		}
	}
	return parts, nil
}

// attachServerResult stores the raw hosted result block on the server tool call
// it belongs to. The result is replayed verbatim and never executed locally.
func attachServerResult(parts []llm.Part, callID, raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: hosted result has no JSON", llm.ErrUnsupportedPart)
	}
	for index := len(parts) - 1; index >= 0; index-- {
		call, ok := parts[index].(*llm.ToolCallPart)
		if !ok || call.EffectiveKind() != llm.ToolKindServer {
			continue
		}
		if callID != "" && call.CallID != callID {
			continue
		}
		state, err := stateOf(call)
		if err != nil {
			return err
		}
		state.ServerResult = json.RawMessage(raw)
		return attachState(call, state)
	}
	return fmt.Errorf("%w: hosted result for unknown server tool call %q", llm.ErrUnsupportedPart, callID)
}

// canonicalInput keeps tool input as raw JSON, defaulting to an empty object.
func canonicalInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 || string(input) == "null" {
		return json.RawMessage("{}")
	}
	return append(json.RawMessage(nil), input...)
}

// marshalInput encodes a decoded streaming value as canonical raw JSON.
func marshalInput(input any) (json.RawMessage, error) {
	if input == nil {
		return json.RawMessage("{}"), nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode tool input: %w", err)
	}
	return canonicalInput(encoded), nil
}

// refusalText returns a stable non-empty reason for a refusal part.
func refusalText(explanation string) string {
	if text := strings.TrimSpace(explanation); text != "" {
		return text
	}
	return "model refused"
}

// mapStopReason maps Anthropic stop reasons to canonical reasons and message
// status. Only a normal turn or a tool request counts as completed.
func mapStopReason(reason anthropic.StopReason) (llm.FinishReason, llm.MessageStatus) {
	switch reason {
	case "", anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return llm.FinishReasonStop, llm.MessageStatusCompleted
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return llm.FinishReasonLength, llm.MessageStatusIncomplete
	case anthropic.StopReasonToolUse:
		return llm.FinishReasonTool, llm.MessageStatusCompleted
	case anthropic.StopReasonRefusal:
		return llm.FinishReasonContentFilter, llm.MessageStatusIncomplete
	case anthropic.StopReasonPauseTurn:
		return llm.FinishReasonStop, llm.MessageStatusIncomplete
	default:
		return llm.FinishReason(strings.ToLower(string(reason))), llm.MessageStatusIncomplete
	}
}

// usageFromMessage converts a complete usage snapshot. The API's total input
// is the sum of fresh, cache-write, and cache-read tokens.
func usageFromMessage(usage anthropic.Usage) llm.Usage {
	return usageFromValues(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheCreationInputTokens,
		usage.CacheReadInputTokens,
		usage.OutputTokensDetails.ThinkingTokens,
	)
}

func usageFromValues(input, output, cacheWrite, cacheRead, thinking int64) llm.Usage {
	prompt := input + cacheWrite + cacheRead
	return llm.Usage{
		PromptTokens:     int(prompt),
		CompletionTokens: int(output),
		TotalTokens:      int(prompt + output),
		ReasoningTokens:  int(thinking),
		CachedTokens:     int(cacheRead),
	}
}
