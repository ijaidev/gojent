package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
)

// Response is the final result of a run.
type Response struct {
	// Text is the final assistant text.
	Text string `json:"text"`
	// Thinking is the final assistant reasoning trace.
	Thinking string `json:"thinking,omitempty"`
	// ToolCalls contains every tool call made during the run.
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	// Messages is the full conversation, including system instructions,
	// session history, input, assistant messages, and tool results.
	Messages []llm.Message `json:"messages"`
	// FinishReason explains why the final model call ended.
	FinishReason llm.FinishReason `json:"finish_reason"`
	// Usage is cumulative token usage across all model calls.
	Usage llm.Usage `json:"usage"`
}

// Unmarshal parses the response text into target, stripping a single Markdown
// JSON code fence when present.
func (r *Response) Unmarshal(target any) error {
	if r == nil {
		return errors.New("agent: response is nil")
	}
	cleaned := cleanJSONText(r.Text)
	if cleaned == "" {
		return errors.New("agent: response text is empty")
	}
	return json.Unmarshal([]byte(cleaned), target)
}

func cleanJSONText(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		if index := strings.Index(trimmed, "\n"); index != -1 {
			trimmed = strings.TrimSpace(trimmed[index+1:])
		}
		if strings.HasSuffix(trimmed, "```") {
			trimmed = strings.TrimSpace(trimmed[:len(trimmed)-3])
		}
	}
	return trimmed
}
