package mockllm_test

import (
	"encoding/json"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestConformance(t *testing.T) {
	defaultUsage := llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}

	parallelCalls := []llm.ToolCall{
		{ItemID: "item-1", CallID: "call-1", Name: "lookup", Input: json.RawMessage(`{"q":"go"}`)},
		{ItemID: "item-2", CallID: "call-2", Name: "clock", Input: json.RawMessage(`{}`)},
	}

	toolErrorRequest := &llm.Request{Messages: []llm.Message{
		llm.UserMessage("check the weather"),
		llm.AssistantMessageWithParts(llm.NewToolCallPart(llm.ToolCall{
			CallID: "call-1",
			Name:   "weather",
			Input:  json.RawMessage(`{"city":"Tokyo"}`),
		})),
		llm.ToolResultsMessage(llm.ToolResult{
			CallID:  "call-1",
			Name:    "weather",
			Status:  llm.ToolStatusError,
			Content: []llm.Part{llm.NewTextPart("upstream timeout")},
		}),
	}}

	fileRequest := &llm.Request{Messages: []llm.Message{
		llm.UserMessageWithParts(
			llm.NewTextPart("describe this image"),
			llm.FileFromBytes([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
		),
	}}

	llmtest.Run(t, func(t *testing.T) llm.Model {
		return mockllm.New("hello world")
	},
		llmtest.Case{
			Name:            "Text",
			Request:         &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:            "hello world",
			Usage:           &defaultUsage,
			FinishReason:    llm.FinishReasonStop,
			RawFinishReason: "stop",
			Status:          llm.MessageStatusCompleted,
		},
		llmtest.Case{
			Name:    "Reasoning",
			Request: &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("answer").ApplyOptions(mockllm.WithReasoning("thinking"))
			},
			Text:         "answer",
			Reasoning:    "thinking",
			FinishReason: llm.FinishReasonStop,
			Status:       llm.MessageStatusCompleted,
		},
		llmtest.Case{
			Name:    "SingleToolCall",
			Request: &llm.Request{Messages: []llm.Message{llm.UserMessage("look it up")}},
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("").ApplyOptions(mockllm.WithToolCalls(parallelCalls[0]))
			},
			ToolCalls:       1,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "tool_calls",
			Status:          llm.MessageStatusCompleted,
		},
		llmtest.Case{
			Name:    "ParallelToolCalls",
			Request: &llm.Request{Messages: []llm.Message{llm.UserMessage("use both")}},
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("").ApplyOptions(mockllm.WithToolCalls(parallelCalls...))
			},
			ToolCalls:       2,
			FinishReason:    llm.FinishReasonTool,
			RawFinishReason: "tool_calls",
			Status:          llm.MessageStatusCompleted,
		},
		llmtest.Case{
			Name:         "InterleavedReasoningTextAndCalls",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("plan")}},
			Text:         "checking now",
			Reasoning:    "think first",
			ToolCalls:    2,
			FinishReason: llm.FinishReasonTool,
			Status:       llm.MessageStatusCompleted,
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("checking now").ApplyOptions(
					mockllm.WithReasoning("think first"),
					mockllm.WithToolCalls(parallelCalls...),
				)
			},
		},
		llmtest.Case{
			Name:         "ToolErrorRequest",
			Request:      toolErrorRequest,
			Text:         "the weather service timed out",
			FinishReason: llm.FinishReasonStop,
			Status:       llm.MessageStatusCompleted,
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("the weather service timed out")
			},
		},
		llmtest.Case{
			Name:         "ImageInput",
			Request:      fileRequest,
			Text:         "a tiny image",
			FinishReason: llm.FinishReasonStop,
			Status:       llm.MessageStatusCompleted,
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("a tiny image")
			},
		},
		llmtest.Case{
			Name:         "ProviderMetadata",
			Request:      &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
			Text:         "hello world",
			FinishReason: llm.FinishReasonStop,
			Status:       llm.MessageStatusCompleted,
			Model: func(t *testing.T) llm.Model {
				return mockllm.New("hello world").ApplyOptions(
					mockllm.WithProviderMetadata("mock", map[string]string{"trace": "abc"}),
				)
			},
		},
	)
}
