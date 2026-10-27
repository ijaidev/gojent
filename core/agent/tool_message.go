package agent

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// canonicalResult converts a tool result into the provider-neutral result
// contract. Tool output content is restricted to text and files.
func canonicalResult(call tool.Call, result tool.Result, status llm.ToolStatus) (llm.ToolResult, error) {
	parts, err := result.Parts()
	if err != nil {
		return llm.ToolResult{}, err
	}
	parts = llm.NonEmptyParts(parts)
	for i, part := range parts {
		switch part.(type) {
		case *llm.TextPart, *llm.FilePart:
		default:
			return llm.ToolResult{}, fmt.Errorf("%w: output part %d is %T", tool.ErrOutputEncode, i, part)
		}
	}
	if len(parts) == 0 {
		parts = []llm.Part{llm.NewTextPart("{}")}
	}
	canonical := llm.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Kind:    call.Kind,
		Status:  status,
		Content: parts,
	}
	if result.Data != nil {
		encoded, encodeErr := json.Marshal(result.Data)
		if encodeErr != nil {
			return llm.ToolResult{}, fmt.Errorf("%w: %w", tool.ErrOutputEncode, encodeErr)
		}
		canonical.Structured = encoded
	}
	return canonical, nil
}

// toolErrorResult builds an error result with a provider-safe payload.
func toolErrorResult(call tool.Call, status llm.ToolStatus, err error) llm.ToolResult {
	return llm.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Kind:    call.Kind,
		Status:  status,
		Content: []llm.Part{llm.NewTextPart(toolErrorPayload(err))},
	}
}

// resultMessage wraps one result in a user message for event observation.
// The runner groups all results for a step before persisting.
func resultMessage(result llm.ToolResult) llm.Message {
	return llm.ToolResultsMessage(result)
}
