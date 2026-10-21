package llm

// UserMessage creates a user message with the given text content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: []Part{TextPart(content)}}
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

// ToolResultMessage creates a tool execution output message for a specific tool call ID.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    []Part{TextPart(content)},
	}
}
