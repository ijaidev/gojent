package google

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// partProviderState is the per-part replay state Gemini needs. It lives in
// PartMeta.Provider["google"] and annotates canonical content; it never
// replaces it (D11). Thought signatures must be replayed on the same part.
type partProviderState struct {
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

// providerState reads Google replay metadata from a canonical part. Missing
// metadata is not an error; malformed metadata fails closed.
func providerState(part llm.Part) (partProviderState, error) {
	var state partProviderState
	if part == nil || part.Meta() == nil {
		return state, nil
	}
	if err := part.Meta().Get(providerName, &state); err != nil {
		if errors.Is(err, llm.ErrNoProviderData) {
			return partProviderState{}, nil
		}
		return partProviderState{}, err
	}
	return state, nil
}

// attachProviderState stores Google replay metadata on a canonical part.
func attachProviderState(part llm.Part, state partProviderState) error {
	if part == nil || part.Meta() == nil || len(state.ThoughtSignature) == 0 {
		return nil
	}
	return part.Meta().Set(providerName, state)
}

// providerDataFor encodes replay metadata for a stream event.
func providerDataFor(state partProviderState) llm.ProviderData {
	if len(state.ThoughtSignature) == 0 {
		return nil
	}
	data := make(llm.ProviderData)
	if err := data.Set(providerName, state); err != nil {
		return nil
	}
	return data
}

// convertPartToGemini translates one canonical part into a Gemini part.
func convertPartToGemini(part llm.Part) (*genai.Part, error) {
	if part == nil {
		return nil, fmt.Errorf("%w: nil part", llm.ErrInvalidPart)
	}

	switch p := part.(type) {
	case *llm.TextPart:
		if p.Text == "" {
			return nil, nil
		}
		state, err := providerState(p)
		if err != nil {
			return nil, err
		}
		converted := genai.NewPartFromText(p.Text)
		converted.ThoughtSignature = state.ThoughtSignature
		return converted, nil

	case *llm.ReasoningPart:
		state, err := providerState(p)
		if err != nil {
			return nil, err
		}
		text := reasoningText(p)
		if text == "" && len(state.ThoughtSignature) == 0 {
			return nil, fmt.Errorf("%w: reasoning part has no text or signature", llm.ErrUnsupportedPart)
		}
		return &genai.Part{
			Text:             text,
			Thought:          true,
			ThoughtSignature: state.ThoughtSignature,
		}, nil

	case *llm.FilePart:
		return convertFilePartToGemini(p)

	case *llm.ToolCallPart:
		if p.EffectiveKind() != llm.ToolKindFunction {
			return nil, fmt.Errorf("%w: google does not support %q tool calls", llm.ErrUnsupported, p.EffectiveKind())
		}
		input, err := toolInputObject(p.Input)
		if err != nil {
			return nil, err
		}
		state, err := providerState(p)
		if err != nil {
			return nil, err
		}
		return &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   p.CallID,
				Name: p.Name,
				Args: input,
			},
			ThoughtSignature: state.ThoughtSignature,
		}, nil

	case *llm.ToolResultPart:
		return convertToolResultToGemini(p.ToolResult, p.Name)

	case *llm.RefusalPart:
		if p.Text == "" {
			return nil, fmt.Errorf("%w: empty refusal", llm.ErrInvalidPart)
		}
		// Gemini has no refusal payload. A refusal is model-authored text on
		// replay, so send it as text rather than dropping the turn.
		return genai.NewPartFromText(p.Text), nil

	default:
		return nil, fmt.Errorf("%w: %T", llm.ErrUnsupportedPartType, part)
	}
}

// reasoningText joins readable reasoning and summaries for providers that
// carry reasoning as a single thought string.
func reasoningText(part *llm.ReasoningPart) string {
	if part.Text != "" {
		return part.Text
	}
	if len(part.Summary) == 0 {
		return ""
	}
	return strings.Join(part.Summary, "\n")
}

func convertFilePartToGemini(part *llm.FilePart) (*genai.Part, error) {
	if err := validateFileMIME(part.MIMEType); err != nil {
		return nil, err
	}

	switch {
	case len(part.Data) > 0:
		return genai.NewPartFromBytes(part.Data, part.MIMEType), nil
	case part.URL != "":
		if !isGeminiFileURI(part.URL) {
			return nil, fmt.Errorf("google: URI %q is not a Gemini file URI (use inline data or the Files API): %w", part.URL, llm.ErrUnsupportedPart)
		}
		return genai.NewPartFromURI(part.URL, part.MIMEType), nil
	case part.FileID != "":
		return nil, fmt.Errorf("google: file_id %q is not a Gemini file URI; upload it and use FilePart.URL: %w", part.FileID, llm.ErrUnsupportedPart)
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
}

func isGeminiFileURI(uri string) bool {
	return strings.HasPrefix(uri, "gs://") ||
		strings.HasPrefix(uri, "https://generativelanguage.googleapis.com/")
}

func validateFileMIME(mimeType string) error {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mimeType == "" {
		return fmt.Errorf("%w: file part is missing a MIME type", llm.ErrInvalidPart)
	}
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		if !isSupportedImageMIME(mimeType) {
			return fmt.Errorf("google: unsupported image MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
	case strings.HasPrefix(mimeType, "audio/"):
		if !isSupportedAudioMIME(mimeType) {
			return fmt.Errorf("google: unsupported audio MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
	case strings.HasPrefix(mimeType, "video/"):
		if !isSupportedVideoMIME(mimeType) {
			return fmt.Errorf("google: unsupported video MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
	default:
		if !isSupportedDocMIME(mimeType) {
			return fmt.Errorf("google: unsupported document MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
	}
	return nil
}

// toolInputObject decodes canonical tool input into the JSON object Gemini
// requires for function arguments.
func toolInputObject(input json.RawMessage) (map[string]any, error) {
	if len(input) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, fmt.Errorf("google: decode tool input: %w", err)
	}
	if value == nil {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: Gemini function arguments must be a JSON object", llm.ErrInvalidToolCall)
	}
	return object, nil
}

// convertToolResultToGemini builds the functionResponse part Gemini expects in
// the user turn that follows a model function call.
func convertToolResultToGemini(result llm.ToolResult, name string) (*genai.Part, error) {
	if result.EffectiveKind() != llm.ToolKindFunction {
		return nil, fmt.Errorf("%w: google does not support %q tool results", llm.ErrUnsupported, result.EffectiveKind())
	}
	switch result.Status {
	case "", llm.ToolStatusCompleted, llm.ToolStatusError, llm.ToolStatusDenied, llm.ToolStatusIncomplete:
	default:
		return nil, fmt.Errorf("%w: google does not support tool result status %q", llm.ErrUnsupported, result.Status)
	}
	if result.CallID == "" {
		return nil, fmt.Errorf("%w: tool result has no call id", llm.ErrInvalidToolResult)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: tool result %q has no name", llm.ErrInvalidToolResult, result.CallID)
	}

	response, err := toolResponseMap(result)
	if err != nil {
		return nil, err
	}

	var media []*genai.FunctionResponsePart
	for index, part := range result.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			continue
		case *llm.FilePart:
			converted, err := convertFilePartToFunctionResponse(p)
			if err != nil {
				return nil, fmt.Errorf("google: tool result content[%d]: %w", index, err)
			}
			media = append(media, converted)
		default:
			return nil, fmt.Errorf("%w: tool result content[%d] is %T", llm.ErrInvalidToolResult, index, part)
		}
	}

	var converted *genai.Part
	if len(media) > 0 {
		converted = genai.NewPartFromFunctionResponseWithParts(name, response, media)
	} else {
		converted = genai.NewPartFromFunctionResponse(name, response)
	}
	if converted == nil || converted.FunctionResponse == nil {
		return nil, fmt.Errorf("google: failed to build function response for %q", name)
	}
	converted.FunctionResponse.ID = result.CallID
	return converted, nil
}

func convertFilePartToFunctionResponse(part *llm.FilePart) (*genai.FunctionResponsePart, error) {
	if err := validateFileMIME(part.MIMEType); err != nil {
		return nil, err
	}
	switch {
	case len(part.Data) > 0:
		return genai.NewFunctionResponsePartFromBytes(part.Data, part.MIMEType), nil
	case part.URL != "":
		return genai.NewFunctionResponsePartFromURI(part.URL, part.MIMEType), nil
	case part.FileID != "":
		return nil, fmt.Errorf("google: function responses need inline data or a URI, not file_id %q: %w", part.FileID, llm.ErrUnsupportedPart)
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
}

// toolResponseMap follows Gemini's documented response convention: "output"
// carries the result and "error" carries failure details.
func toolResponseMap(result llm.ToolResult) (map[string]any, error) {
	response := make(map[string]any)
	if len(result.Structured) > 0 {
		var structured any
		if err := json.Unmarshal(result.Structured, &structured); err != nil {
			return nil, fmt.Errorf("google: decode structured tool result: %w", err)
		}
		if object, ok := structured.(map[string]any); ok {
			for key, value := range object {
				response[key] = value
			}
		} else {
			response["output"] = structured
		}
	}

	text := result.Text()
	if text != "" {
		if _, exists := response["output"]; !exists {
			response["output"] = text
		}
	}
	if _, exists := response["output"]; !exists {
		response["output"] = ""
	}

	switch result.Status {
	case llm.ToolStatusError, llm.ToolStatusDenied, llm.ToolStatusIncomplete:
		detail := text
		if detail == "" {
			detail = string(result.Status)
		}
		response["error"] = detail
	}
	return response, nil
}
