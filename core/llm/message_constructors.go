package llm

// UserMessage creates a user message with the given text content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: []Part{TextPart(content)}}
}

// UserMessageWithMedia creates a user message combining a text prompt with one or more media parts.
func UserMessageWithMedia(prompt string, media ...Part) Message {
	parts := make([]Part, 0, len(media)+1)
	if prompt != "" {
		parts = append(parts, TextPart(prompt))
	}
	parts = append(parts, media...)
	return Message{
		Role:    RoleUser,
		Content: parts,
	}
}

// UserMessageWithParts creates a user message containing explicit parts.
func UserMessageWithParts(parts ...Part) Message {
	return Message{
		Role:    RoleUser,
		Content: parts,
	}
}

// SystemMessage creates a system instruction message with the given content.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: []Part{TextPart(content)}}
}

// AssistantMessage creates an assistant message with the given content.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: []Part{TextPart(content)}}
}

// AssistantMessageWithParts creates an assistant message with explicit parts.
func AssistantMessageWithParts(parts ...Part) Message {
	return Message{
		Role:    RoleAssistant,
		Content: parts,
	}
}

// ToolResultMessage creates a tool execution output message for a specific tool call ID.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    []Part{TextPart(content)},
	}
}

// ToolResultMessageWithParts creates a tool execution output message containing rich multipart results.
func ToolResultMessageWithParts(toolCallID, name string, parts ...Part) Message {
	return Message{
		Role:       RoleTool,
		Name:       name,
		ToolCallID: toolCallID,
		Content:    parts,
	}
}
