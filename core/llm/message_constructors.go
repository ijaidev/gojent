package llm

// UserMessage creates a user message with text content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: NonEmptyParts([]Part{TextPart(content)})}
}

// UserMessageWithParts creates a user message containing explicit parts.
func UserMessageWithParts(parts ...Part) Message {
	return Message{Role: RoleUser, Content: NonEmptyParts(parts)}
}

// SystemMessage creates a system instruction message.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: NonEmptyParts([]Part{TextPart(content)})}
}

// AssistantMessage creates an assistant message with text content.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: NonEmptyParts([]Part{TextPart(content)})}
}

// ToolResultMessage creates a tool result for a specific call.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    NonEmptyParts([]Part{TextPart(content)}),
	}
}

// ToolErrorMessage creates an error tool result for a specific call.
func ToolErrorMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		IsError:    true,
		Content:    NonEmptyParts([]Part{TextPart(content)}),
	}
}
