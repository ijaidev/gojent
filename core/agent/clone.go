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
	merged[last].Content = llm.NonEmptyParts(append(cloneParts(merged[last].Content), media...))
	return merged
}

func cloneParts(parts []llm.Part) []llm.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]llm.Part, len(parts))
	for i, part := range parts {
		out[i] = part
		out[i].Data = append([]byte(nil), part.Data...)
	}
	return out
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
