package agent

import (
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

func mergeMedia(messages []llm.Message, media []llm.Part) []llm.Message {
	if len(media) == 0 {
		return messages
	}
	if len(messages) == 0 {
		return []llm.Message{llm.UserMessageWithParts(media...)}
	}
	last := len(messages) - 1
	if messages[last].Role != llm.RoleUser {
		merged := make([]llm.Message, 0, len(messages)+1)
		merged = append(merged, messages...)
		return append(merged, llm.UserMessageWithParts(media...))
	}
	merged := llm.CloneMessages(messages)
	media = llm.CloneParts(media)
	merged[last].Content = llm.NonEmptyParts(append(llm.CloneParts(merged[last].Content), media...))
	return merged
}

func cloneSession(s *session.Session) *session.Session {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
