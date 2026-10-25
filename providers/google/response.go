package google

import (
	"encoding/json"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// blockedResponseError returns ErrResponseBlocked when Gemini produced no
// candidates because the prompt was blocked for safety or policy reasons.
// A response with candidates is never treated as blocked here; candidate-level
// finish reasons surface through FinishReason instead.
func blockedResponseError(resp *genai.GenerateContentResponse) error {
	if resp == nil || len(resp.Candidates) > 0 {
		return nil
	}
	feedback := resp.PromptFeedback
	if feedback == nil || feedback.BlockReason == "" {
		return nil
	}
	reason := feedback.BlockReasonMessage
	if reason == "" {
		reason = string(feedback.BlockReason)
	}
	return fmt.Errorf("%w: %s", ErrResponseBlocked, reason)
}

// firstCandidate returns the only candidate Gojent exposes. Multi-candidate
// responses are out of scope (D21).
func firstCandidate(resp *genai.GenerateContentResponse) *genai.Candidate {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	return resp.Candidates[0]
}

func usageOf(resp *genai.GenerateContentResponse) llm.Usage {
	if resp == nil {
		return llm.Usage{}
	}
	return usageOfMetadata(resp.UsageMetadata)
}

func usageOfMetadata(metadata *genai.GenerateContentResponseUsageMetadata) llm.Usage {
	if metadata == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		PromptTokens:     int(metadata.PromptTokenCount),
		CompletionTokens: int(metadata.CandidatesTokenCount),
		TotalTokens:      int(metadata.TotalTokenCount),
		ReasoningTokens:  int(metadata.ThoughtsTokenCount),
		CachedTokens:     int(metadata.CachedContentTokenCount),
	}
}

// messageStatus maps a terminal reason to canonical message status. Only a
// normal stop or a tool request counts as a completed turn.
func messageStatus(reason llm.FinishReason) llm.MessageStatus {
	switch reason {
	case llm.FinishReasonStop, llm.FinishReasonTool, "":
		return llm.MessageStatusCompleted
	default:
		return llm.MessageStatusIncomplete
	}
}

// canonicalResponse converts a non-streaming Gemini response into canonical
// form. Only the first candidate is read.
func (m *Model) canonicalResponse(resp *genai.GenerateContentResponse) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("%w: nil response", llm.ErrInvalidMessage)
	}

	message := llm.Message{Role: llm.RoleAssistant}
	candidate := firstCandidate(resp)
	rawFinish := ""
	if candidate != nil {
		if candidate.Content != nil {
			for index, part := range candidate.Content.Parts {
				if signatureOnly(part) {
					attached, err := attachSignature(message.Content, part.ThoughtSignature)
					if err != nil {
						return nil, err
					}
					message.Content = attached
					continue
				}
				converted, err := canonicalPart(part, index)
				if err != nil {
					return nil, err
				}
				if converted != nil {
					message.Content = append(message.Content, converted)
				}
			}
		}
		rawFinish = string(candidate.FinishReason)
	}
	if len(message.Content) == 0 {
		return nil, fmt.Errorf("google: response contains no content: %w", llm.ErrInvalidMessage)
	}

	finish := mapFinishReason(genai.FinishReason(rawFinish))
	if finish == "" {
		finish = llm.FinishReasonStop
	}
	if finish == llm.FinishReasonStop && len(message.ToolCalls()) > 0 {
		finish = llm.FinishReasonTool
	}
	status := messageStatus(finish)
	message.Status = status

	modelID := resp.ModelVersion
	if modelID == "" {
		modelID = m.modelID
	}
	return &llm.Response{
		ID:              resp.ResponseID,
		Model:           modelID,
		Message:         message,
		Status:          status,
		FinishReason:    finish,
		RawFinishReason: rawFinish,
		Usage:           usageOf(resp),
	}, nil
}

// canonicalPart converts one Gemini part into canonical content.
func canonicalPart(part *genai.Part, index int) (llm.Part, error) {
	if part == nil {
		return nil, nil
	}
	if err := rejectServerPart(part); err != nil {
		return nil, err
	}

	state := partProviderState{ThoughtSignature: part.ThoughtSignature}

	switch {
	case part.FunctionCall != nil:
		call := part.FunctionCall
		input, err := json.Marshal(call.Args)
		if err != nil {
			return nil, fmt.Errorf("google: encode function call args for %q: %w", call.Name, err)
		}
		if len(call.Args) == 0 {
			input = json.RawMessage("{}")
		}
		callID := call.ID
		if callID == "" {
			// Gemini does not always assign function-call IDs. Persist a
			// deterministic one so results can reference it after a store
			// round-trip (invariant 13).
			callID = fmt.Sprintf("call_%d", index)
		}
		converted := llm.NewToolCallPart(llm.ToolCall{
			CallID: callID,
			Name:   call.Name,
			Kind:   llm.ToolKindFunction,
			Input:  input,
			Status: llm.ToolStatusCompleted,
		})
		if err := attachProviderState(converted, state); err != nil {
			return nil, err
		}
		return converted, nil

	case part.InlineData != nil:
		if part.InlineData.MIMEType == "" || len(part.InlineData.Data) == 0 {
			return nil, fmt.Errorf("%w: inline data is missing a MIME type or bytes", llm.ErrInvalidPart)
		}
		converted := llm.FileFromBytes(part.InlineData.Data, part.InlineData.MIMEType)
		converted.Filename = part.InlineData.DisplayName
		return converted, nil

	case part.FileData != nil:
		if part.FileData.MIMEType == "" || part.FileData.FileURI == "" {
			return nil, fmt.Errorf("%w: file data is missing a MIME type or URI", llm.ErrInvalidPart)
		}
		converted := llm.FileFromURL(part.FileData.FileURI, part.FileData.MIMEType)
		converted.Filename = part.FileData.DisplayName
		return converted, nil

	case part.Thought:
		if part.Text == "" && len(part.ThoughtSignature) == 0 {
			return nil, nil
		}
		converted := llm.NewReasoningPart(part.Text)
		if err := attachProviderState(converted, state); err != nil {
			return nil, err
		}
		return converted, nil

	case part.Text != "":
		converted := llm.NewTextPart(part.Text)
		if err := attachProviderState(converted, state); err != nil {
			return nil, err
		}
		return converted, nil

	case len(part.ThoughtSignature) > 0:
		// A signature without text still has to be replayed on the same part.
		// Reasoning is the only canonical part that permits empty text with
		// provider metadata.
		converted := llm.NewReasoningPart("")
		if err := attachProviderState(converted, state); err != nil {
			return nil, err
		}
		return converted, nil

	default:
		return nil, nil
	}
}

// signatureOnly reports whether a Gemini part carries nothing but a thought
// signature, which belongs to the preceding content part.
func signatureOnly(part *genai.Part) bool {
	if part == nil || len(part.ThoughtSignature) == 0 {
		return false
	}
	return part.FunctionCall == nil &&
		part.InlineData == nil &&
		part.FileData == nil &&
		part.Text == "" &&
		!part.Thought
}

// attachSignature stores a signature on the preceding text-like part. When
// there is none, it keeps the signature as an empty reasoning part so replay
// does not lose it.
func attachSignature(parts []llm.Part, signature []byte) ([]llm.Part, error) {
	for index := len(parts) - 1; index >= 0; index-- {
		switch parts[index].(type) {
		case *llm.TextPart, *llm.ReasoningPart:
			if err := attachProviderState(parts[index], partProviderState{ThoughtSignature: signature}); err != nil {
				return nil, err
			}
			return parts, nil
		}
	}
	reasoning := llm.NewReasoningPart("")
	if err := attachProviderState(reasoning, partProviderState{ThoughtSignature: signature}); err != nil {
		return nil, err
	}
	return append(parts, reasoning), nil
}

// rejectServerPart fails closed for hosted-tool and code-execution parts the
// adapter does not advertise.
func rejectServerPart(part *genai.Part) error {
	switch {
	case part.FunctionResponse != nil:
		return fmt.Errorf("%w: function response in model output", llm.ErrUnsupportedPart)
	case part.ToolCall != nil, part.ToolResponse != nil:
		return fmt.Errorf("%w: server-side tool parts", llm.ErrUnsupportedPart)
	case part.ExecutableCode != nil, part.CodeExecutionResult != nil:
		return fmt.Errorf("%w: code execution parts", llm.ErrUnsupportedPart)
	case part.AudioTranscription != nil, part.SpeechMetadata != nil:
		return fmt.Errorf("%w: audio transcription or speech metadata", llm.ErrUnsupportedPart)
	default:
		return nil
	}
}
