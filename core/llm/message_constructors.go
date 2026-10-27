package llm

// UserMessage creates a user message with text content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: NonEmptyParts([]Part{NewTextPart(content)})}
}

// UserMessageWithParts creates a user message containing explicit parts.
func UserMessageWithParts(parts ...Part) Message {
	return Message{Role: RoleUser, Content: NonEmptyParts(parts)}
}

// SystemMessage creates a system instruction message.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: NonEmptyParts([]Part{NewTextPart(content)})}
}

// DeveloperMessage creates a developer instruction message.
func DeveloperMessage(content string) Message {
	return Message{Role: RoleDeveloper, Content: NonEmptyParts([]Part{NewTextPart(content)})}
}

// AssistantMessage creates an assistant message with text content.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: NonEmptyParts([]Part{NewTextPart(content)})}
}

// AssistantMessageWithParts creates an assistant message containing explicit
// parts, preserving their order.
func AssistantMessageWithParts(parts ...Part) Message {
	return Message{Role: RoleAssistant, Content: NonEmptyParts(parts)}
}

// ToolResultsMessage groups results for one assistant turn in a single user
// message, in the given order.
func ToolResultsMessage(results ...ToolResult) Message {
	parts := make([]Part, 0, len(results))
	for index := range results {
		parts = append(parts, NewToolResultPart(results[index]))
	}
	return Message{Role: RoleUser, Content: NonEmptyParts(parts)}
}
