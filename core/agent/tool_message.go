package agent

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

func resultMessage(call tool.Call, result tool.Result) (llm.Message, error) {
	parts, err := result.Parts()
	if err != nil {
		return llm.Message{}, err
	}
	if len(parts) == 0 {
		parts = []llm.Part{llm.TextPart("{}")}
	}
	var data json.RawMessage
	if result.Data != nil {
		encoded, encodeErr := json.Marshal(result.Data)
		if encodeErr != nil {
			return llm.Message{}, fmt.Errorf("%w: %w", tool.ErrOutputEncode, encodeErr)
		}
		data = encoded
	}
	return llm.Message{
		Role:       llm.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    parts,
		Data:       data,
	}, nil
}

func toolErrorMessage(call tool.Call, err error) llm.Message {
	return llm.Message{
		Role:       llm.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    []llm.Part{llm.TextPart(toolErrorPayload(err))},
		IsError:    true,
	}
}
