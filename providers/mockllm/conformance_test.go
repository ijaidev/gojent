package mockllm_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestConformance(t *testing.T) {
	usage := llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
	llmtest.Run(t, func(t *testing.T) llm.Model {
		return mockllm.New("hello world").ApplyOptions(mockllm.WithUsage(usage))
	},
		llmtest.Case{
			Name:         "Text",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:         "hello world",
			FinishReason: llm.FinishReasonStop,
		},
	)

	t.Run("ToolCalls", func(t *testing.T) {
		calls := []llm.ToolCall{
			{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`},
			{Index: 1, ID: "call-2", Name: "time", Arguments: `{}`},
		}
		llmtest.Run(t, func(t *testing.T) llm.Model {
			return mockllm.New("").ApplyOptions(
				mockllm.WithToolCalls(calls...),
				mockllm.WithUsage(usage),
			)
		},
			llmtest.Case{
				Name:         "ToolCalls",
				Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("go")}},
				ToolCalls:    2,
				FinishReason: llm.FinishReasonTool,
			},
		)
	})
}
