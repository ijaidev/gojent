package google

import (
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

const providerName = "google"

// convertMessages translates canonical messages into Gemini contents and a
// system instruction. System and developer messages are hoisted into the
// instruction in transcript order; assistant turns become the model role and
// tool results stay in the user turn that follows them.
func convertMessages(messages []llm.Message) (contents []*genai.Content, systemInstruction *genai.Content, err error) {
	var systemParts []*genai.Part
	callNames := make(map[string]string)

	for i := range messages {
		message := &messages[i]
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			for index, part := range message.Content {
				text, ok := part.(*llm.TextPart)
				if !ok {
					return nil, nil, fmt.Errorf("google: system message content[%d] is %T, want text: %w", index, part, llm.ErrUnsupportedPartType)
				}
				if text.Text == "" {
					continue
				}
				systemParts = append(systemParts, genai.NewPartFromText(text.Text))
			}
			continue
		case llm.RoleAssistant:
			for _, call := range message.ToolCalls() {
				if call.CallID != "" && call.Name != "" {
					callNames[call.CallID] = call.Name
				}
			}
		case llm.RoleUser:
		default:
			return nil, nil, fmt.Errorf("%w: role %q", llm.ErrInvalidMessage, message.Role)
		}

		parts := make([]*genai.Part, 0, len(message.Content))
		for _, part := range message.Content {
			var (
				converted *genai.Part
				err       error
			)
			if result, ok := part.(*llm.ToolResultPart); ok {
				name := result.Name
				if name == "" {
					name = callNames[result.CallID]
				}
				converted, err = convertToolResultToGemini(result.ToolResult, name)
			} else {
				converted, err = convertPartToGemini(part)
			}
			if err != nil {
				return nil, nil, err
			}
			if converted != nil {
				parts = append(parts, converted)
			}
		}
		if len(parts) == 0 {
			continue
		}

		role := genai.RoleUser
		if message.Role == llm.RoleAssistant {
			role = genai.RoleModel
		}
		contents = append(contents, &genai.Content{Role: role, Parts: parts})
	}

	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{Parts: systemParts}
	}
	return contents, systemInstruction, nil
}
