package google

import (
	"strings"

	"google.golang.org/genai"
)

// extractCandidateTextAndThinking retrieves output text and thought process across candidates separately.
func extractCandidateTextAndThinking(candidates []*genai.Candidate) (text string, thinking string) {
	var textSb strings.Builder
	var thinkSb strings.Builder
	for _, cand := range candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Thought {
					thinkSb.WriteString(part.Text)
				} else if part.Text != "" {
					textSb.WriteString(part.Text)
				}
			}
		}
	}
	return textSb.String(), thinkSb.String()
}

// extractRawGeminiContent attempts to retrieve the original *genai.Content from a raw response payload,
// filtering out any empty parts that lack initialized 'data' fields required by the Gemini API.
func extractRawGeminiContent(raw any) *genai.Content {
	if raw == nil {
		return nil
	}
	var rawParts []*genai.Part
	var role string
	switch r := raw.(type) {
	case *genai.GenerateContentResponse:
		if len(r.Candidates) > 0 && r.Candidates[0].Content != nil {
			role = r.Candidates[0].Content.Role
			rawParts = r.Candidates[0].Content.Parts
		}
	case *genai.Content:
		role = r.Role
		rawParts = r.Parts
	}

	if len(rawParts) == 0 {
		return nil
	}

	var validParts []*genai.Part
	for _, p := range rawParts {
		if isPartValid(p) {
			validParts = append(validParts, p)
		}
	}
	if len(validParts) == 0 {
		return nil
	}

	if role == "" {
		role = genai.RoleModel
	}
	return &genai.Content{
		Role:  role,
		Parts: validParts,
	}
}

// isPartValid returns true if the Part contains an initialized payload for the 'data' oneof.
func isPartValid(p *genai.Part) bool {
	if p == nil {
		return false
	}
	return p.Text != "" ||
		p.FunctionCall != nil ||
		p.FunctionResponse != nil ||
		p.InlineData != nil ||
		p.FileData != nil ||
		p.ExecutableCode != nil ||
		p.CodeExecutionResult != nil
}
