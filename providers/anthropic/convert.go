package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/ijaidev/gojent/core/llm"
)

// partState is the per-part replay state Anthropic needs. It lives in
// PartMeta.Provider["anthropic"] and annotates canonical content; it never
// replaces it (D11).
//
// Thinking signatures and redacted-thinking payloads must be replayed exactly.
// Hosted-tool result blocks are recorded verbatim so a paused turn can be sent
// back without the client executing the tool.
type partState struct {
	// Signature is the thinking block signature, byte for byte.
	Signature string `json:"signature,omitempty"`
	// RedactedData is the opaque redacted_thinking payload.
	RedactedData string `json:"redacted_data,omitempty"`
	// ServerResult is the raw hosted-tool result block that followed this
	// server call.
	ServerResult json.RawMessage `json:"server_result,omitempty"`
}

// empty reports whether the state carries nothing to replay. RawMessage is not
// comparable, so the struct cannot be compared to its zero value.
func (s partState) empty() bool {
	return s.Signature == "" && s.RedactedData == "" && len(s.ServerResult) == 0
}

// stateOf reads Anthropic replay metadata from a canonical part. Missing
// metadata is not an error; malformed metadata fails closed.
func stateOf(part llm.Part) (partState, error) {
	var state partState
	if part == nil || part.Meta() == nil {
		return state, nil
	}
	if err := part.Meta().Get(providerName, &state); err != nil {
		if errors.Is(err, llm.ErrNoProviderData) {
			return partState{}, nil
		}
		return partState{}, err
	}
	return state, nil
}

// attachState stores Anthropic replay metadata on a canonical part.
func attachState(part llm.Part, state partState) error {
	if part == nil || part.Meta() == nil {
		return nil
	}
	if state.empty() {
		return nil
	}
	return part.Meta().Set(providerName, state)
}

// providerDataFor encodes replay metadata for a stream event.
func providerDataFor(state partState) llm.ProviderData {
	if state.empty() {
		return nil
	}
	data := make(llm.ProviderData)
	if err := data.Set(providerName, state); err != nil {
		return nil
	}
	return data
}

// convertMessages translates canonical messages into Anthropic messages and
// top-level system blocks.
//
// System and developer messages are hoisted in transcript order. Consecutive
// messages with the same role are merged because the API expects alternating
// turns anyway, and merging avoids empty content arrays.
func convertMessages(messages []llm.Message) ([]anthropic.MessageParam, []anthropic.TextBlockParam, error) {
	var system []anthropic.TextBlockParam
	out := make([]anthropic.MessageParam, 0, len(messages))

	for index := range messages {
		message := &messages[index]
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			for partIndex, part := range message.Content {
				text, ok := part.(*llm.TextPart)
				if !ok {
					return nil, nil, fmt.Errorf("anthropic: system message content[%d] is %T, want text: %w", partIndex, part, llm.ErrUnsupportedPartType)
				}
				if text.Text == "" {
					continue
				}
				system = append(system, anthropic.TextBlockParam{Text: text.Text})
			}

		case llm.RoleUser:
			content, err := convertUserContent(message.Content)
			if err != nil {
				return nil, nil, err
			}
			out = appendContent(out, anthropic.MessageParamRoleUser, content)

		case llm.RoleAssistant:
			content, err := convertAssistantContent(message.Content)
			if err != nil {
				return nil, nil, err
			}
			out = appendContent(out, anthropic.MessageParamRoleAssistant, content)

		default:
			return nil, nil, fmt.Errorf("%w: role %q", llm.ErrInvalidMessage, message.Role)
		}
	}

	if len(out) == 0 {
		return nil, nil, ErrMessagesRequired
	}
	return out, system, nil
}

// appendContent merges consecutive same-role turns.
func appendContent(out []anthropic.MessageParam, role anthropic.MessageParamRole, content []anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if len(content) == 0 {
		return out
	}
	if n := len(out); n > 0 && out[n-1].Role == role {
		out[n-1].Content = append(out[n-1].Content, content...)
		return out
	}
	return append(out, anthropic.MessageParam{Role: role, Content: content})
}

// convertUserContent translates one user turn. Tool results stay in the user
// turn as tool_result blocks; there is no tool role (D5, D17).
func convertUserContent(parts []llm.Part) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for index, part := range parts {
		switch p := part.(type) {
		case *llm.TextPart:
			if p.Text == "" {
				continue
			}
			out = append(out, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{Text: p.Text},
			})
		case *llm.FilePart:
			block, err := convertFileBlock(p)
			if err != nil {
				return nil, fmt.Errorf("anthropic: user content[%d]: %w", index, err)
			}
			out = append(out, block)
		case *llm.ToolResultPart:
			block, err := convertToolResultBlock(&p.ToolResult)
			if err != nil {
				return nil, fmt.Errorf("anthropic: user content[%d]: %w", index, err)
			}
			out = append(out, block)
		default:
			return nil, fmt.Errorf("%w: user content[%d] is %T", llm.ErrUnsupportedPartType, index, part)
		}
	}
	return out, nil
}

// convertAssistantContent translates one assistant turn.
func convertAssistantContent(parts []llm.Part) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for index, part := range parts {
		switch p := part.(type) {
		case *llm.TextPart:
			if p.Text == "" {
				continue
			}
			out = append(out, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{Text: p.Text},
			})
		case *llm.ReasoningPart:
			block, err := convertReasoningBlock(p)
			if err != nil {
				return nil, fmt.Errorf("anthropic: assistant content[%d]: %w", index, err)
			}
			out = append(out, block)
		case *llm.ToolCallPart:
			blocks, err := convertToolCallBlocks(p)
			if err != nil {
				return nil, fmt.Errorf("anthropic: assistant content[%d]: %w", index, err)
			}
			out = append(out, blocks...)
		case *llm.RefusalPart:
			if p.Text == "" {
				return nil, fmt.Errorf("%w: assistant content[%d] is an empty refusal", llm.ErrInvalidPart, index)
			}
			// Messages has no refusal block. A refusal is model-authored text on
			// replay, so send it as text rather than dropping the turn.
			out = append(out, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{Text: p.Text},
			})
		default:
			return nil, fmt.Errorf("%w: assistant content[%d] is %T", llm.ErrUnsupportedPartType, index, part)
		}
	}
	return out, nil
}

// convertReasoningBlock rebuilds a thinking or redacted_thinking block. The
// signature must survive byte for byte and in its original position.
func convertReasoningBlock(part *llm.ReasoningPart) (anthropic.ContentBlockParamUnion, error) {
	state, err := stateOf(part)
	if err != nil {
		return anthropic.ContentBlockParamUnion{}, err
	}
	if state.RedactedData != "" {
		return anthropic.ContentBlockParamUnion{
			OfRedactedThinking: &anthropic.RedactedThinkingBlockParam{
				Data: state.RedactedData,
			},
		}, nil
	}
	if state.Signature == "" {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"%w: reasoning part has no Anthropic thinking signature", llm.ErrUnsupportedPart)
	}
	return anthropic.ContentBlockParamUnion{
		OfThinking: &anthropic.ThinkingBlockParam{
			Thinking:  part.Text,
			Signature: state.Signature,
		},
	}, nil
}

// convertToolCallBlocks rebuilds a tool_use or server_tool_use block. A hosted
// call also replays its recorded result block, which the client never executed.
func convertToolCallBlocks(part *llm.ToolCallPart) ([]anthropic.ContentBlockParamUnion, error) {
	call := &part.ToolCall
	if call.CallID == "" {
		return nil, fmt.Errorf("%w: tool call %q has no call id", llm.ErrInvalidToolCall, call.Name)
	}
	if call.Name == "" {
		return nil, fmt.Errorf("%w: tool call %q has no name", llm.ErrInvalidToolCall, call.CallID)
	}
	input, err := toolInputValue(call.Input)
	if err != nil {
		return nil, err
	}

	state, err := stateOf(part)
	if err != nil {
		return nil, err
	}

	switch call.EffectiveKind() {
	case llm.ToolKindFunction:
		return []anthropic.ContentBlockParamUnion{{
			OfToolUse: &anthropic.ToolUseBlockParam{
				ID:    call.CallID,
				Name:  call.Name,
				Input: input,
			},
		}}, nil

	case llm.ToolKindServer:
		blocks := []anthropic.ContentBlockParamUnion{{
			OfServerToolUse: &anthropic.ServerToolUseBlockParam{
				ID:    call.CallID,
				Name:  anthropic.ServerToolUseBlockParamName(call.Name),
				Input: input,
			},
		}}
		if len(state.ServerResult) > 0 {
			result, err := hostedResultBlock(state.ServerResult, call.Name)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, result)
		}
		return blocks, nil

	default:
		return nil, fmt.Errorf("%w: anthropic does not support %q tool calls", llm.ErrUnsupported, call.EffectiveKind())
	}
}

// hostedResultTypes are the result blocks a hosted tool can return. They are
// recorded verbatim and replayed without local execution (D9).
var hostedResultTypes = map[string]struct{}{
	"web_search_tool_result":                 {},
	"web_fetch_tool_result":                  {},
	"code_execution_tool_result":             {},
	"bash_code_execution_tool_result":        {},
	"text_editor_code_execution_tool_result": {},
	"tool_search_tool_result":                {},
	"container_upload":                       {},
}

// hostedResultBlock decodes one recorded hosted-tool result block. The replay
// state is untrusted input, so the block type is checked before decoding.
func hostedResultBlock(raw json.RawMessage, toolName string) (anthropic.ContentBlockParamUnion, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: decode recorded server result for %q: %w", toolName, err)
	}
	if _, ok := hostedResultTypes[probe.Type]; !ok {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"%w: recorded server result type %q for %q", llm.ErrUnsupportedPart, probe.Type, toolName)
	}
	var block anthropic.ContentBlockParamUnion
	if err := json.Unmarshal(raw, &block); err != nil {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: decode recorded server result for %q: %w", toolName, err)
	}
	if !isHostedResultBlock(block) {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("%w: recorded server result for %q", llm.ErrUnsupportedPart, toolName)
	}
	return block, nil
}

// isHostedResultBlock reports whether a decoded union holds a hosted result.
func isHostedResultBlock(block anthropic.ContentBlockParamUnion) bool {
	return block.OfWebSearchToolResult != nil ||
		block.OfWebFetchToolResult != nil ||
		block.OfCodeExecutionToolResult != nil ||
		block.OfBashCodeExecutionToolResult != nil ||
		block.OfTextEditorCodeExecutionToolResult != nil ||
		block.OfToolSearchToolResult != nil ||
		block.OfContainerUpload != nil
}

// convertToolResultBlock builds a tool_result block from a canonical result.
func convertToolResultBlock(result *llm.ToolResult) (anthropic.ContentBlockParamUnion, error) {
	if result.CallID == "" {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("%w: tool result has no call id", llm.ErrInvalidToolResult)
	}
	if result.EffectiveKind() != llm.ToolKindFunction {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"%w: anthropic does not support %q tool results", llm.ErrUnsupported, result.EffectiveKind())
	}
	switch result.Status {
	case "", llm.ToolStatusCompleted, llm.ToolStatusError, llm.ToolStatusDenied, llm.ToolStatusIncomplete:
	default:
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"%w: anthropic does not support tool result status %q", llm.ErrUnsupported, result.Status)
	}

	content := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(result.Content)+1)
	for index, part := range result.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			content = append(content, anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: p.Text},
			})
		case *llm.FilePart:
			converted, err := convertToolResultFile(p)
			if err != nil {
				return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: tool result content[%d]: %w", index, err)
			}
			content = append(content, converted)
		default:
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
				"%w: tool result content[%d] is %T", llm.ErrInvalidToolResult, index, part)
		}
	}

	// The API has no structured-output field. Serialize it as an extra text
	// block so the model can see it alongside the human-readable content.
	if len(result.Structured) > 0 {
		content = append(content, anthropic.ToolResultBlockParamContentUnion{
			OfText: &anthropic.TextBlockParam{Text: string(result.Structured)},
		})
	}
	// Input requests describe workflow state Anthropic cannot express.
	if len(result.InputRequests) > 0 {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"%w: anthropic does not support tool input requests", llm.ErrUnsupported)
	}
	if len(content) == 0 {
		content = append(content, anthropic.ToolResultBlockParamContentUnion{
			OfText: &anthropic.TextBlockParam{Text: ""},
		})
	}

	block := &anthropic.ToolResultBlockParam{
		ToolUseID: result.CallID,
		Content:   content,
	}
	if isErrorStatus(result.Status) {
		block.IsError = param.NewOpt(true)
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: block}, nil
}

// isErrorStatus reports whether a canonical status should set is_error.
func isErrorStatus(status llm.ToolStatus) bool {
	switch status {
	case llm.ToolStatusError, llm.ToolStatusDenied, llm.ToolStatusIncomplete:
		return true
	default:
		return false
	}
}

// toolInputValue decodes canonical tool input into the JSON object the API
// requires. Empty input becomes an empty object.
func toolInputValue(input json.RawMessage) (any, error) {
	if len(input) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, fmt.Errorf("anthropic: decode tool input: %w", err)
	}
	if value == nil {
		return map[string]any{}, nil
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: Anthropic tool input must be a JSON object", llm.ErrInvalidToolCall)
	}
	return value, nil
}

// convertFileBlock translates a canonical file part into an image or document
// block. MIMEType decides the encoding (D2, D3).
func convertFileBlock(part *llm.FilePart) (anthropic.ContentBlockParamUnion, error) {
	mimeType := normalizeMIME(part.MIMEType)
	if mimeType == "" {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("%w: file part is missing a MIME type", llm.ErrInvalidPart)
	}
	sources, err := fileSourcesFor(part, mimeType)
	if err != nil {
		return anthropic.ContentBlockParamUnion{}, err
	}
	return sources.block(), nil
}

// convertToolResultFile translates a file nested in a tool result.
func convertToolResultFile(part *llm.FilePart) (anthropic.ToolResultBlockParamContentUnion, error) {
	mimeType := normalizeMIME(part.MIMEType)
	if mimeType == "" {
		return anthropic.ToolResultBlockParamContentUnion{}, fmt.Errorf("%w: file part is missing a MIME type", llm.ErrInvalidPart)
	}
	sources, err := fileSourcesFor(part, mimeType)
	if err != nil {
		return anthropic.ToolResultBlockParamContentUnion{}, err
	}
	switch {
	case sources.image != nil:
		return anthropic.ToolResultBlockParamContentUnion{OfImage: sources.image}, nil
	case sources.document != nil:
		return anthropic.ToolResultBlockParamContentUnion{OfDocument: sources.document}, nil
	default:
		return anthropic.ToolResultBlockParamContentUnion{}, fmt.Errorf("%w: file part", llm.ErrUnsupportedPart)
	}
}

// fileSourcesFor holds the mutually exclusive Anthropic encodings for one file.
type fileEncoding struct {
	image    *anthropic.ImageBlockParam
	document *anthropic.DocumentBlockParam
}

func (s fileEncoding) block() anthropic.ContentBlockParamUnion {
	if s.image != nil {
		return anthropic.ContentBlockParamUnion{OfImage: s.image}
	}
	return anthropic.ContentBlockParamUnion{OfDocument: s.document}
}

func fileSourcesFor(part *llm.FilePart, mimeType string) (fileEncoding, error) {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		image, err := imageSource(part, mimeType)
		if err != nil {
			return fileEncoding{}, err
		}
		return fileEncoding{image: image}, nil

	case mimeType == "application/pdf" || mimeType == "text/plain":
		document, err := documentSource(part, mimeType)
		if err != nil {
			return fileEncoding{}, err
		}
		return fileEncoding{document: document}, nil

	case strings.HasPrefix(mimeType, "audio/"):
		return fileEncoding{}, fmt.Errorf("%w: Anthropic does not accept audio", llm.ErrUnsupportedPartType)
	case strings.HasPrefix(mimeType, "video/"):
		return fileEncoding{}, fmt.Errorf("%w: Anthropic does not accept video", llm.ErrUnsupportedPartType)
	default:
		return fileEncoding{}, fmt.Errorf("anthropic: unsupported document MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
	}
}

func imageSource(part *llm.FilePart, mimeType string) (*anthropic.ImageBlockParam, error) {
	block := &anthropic.ImageBlockParam{}
	switch {
	case len(part.Data) > 0:
		mediaType, err := imageMediaType(mimeType)
		if err != nil {
			return nil, err
		}
		block.Source = anthropic.ImageBlockParamSourceUnion{
			OfBase64: &anthropic.Base64ImageSourceParam{
				Data:      base64.StdEncoding.EncodeToString(part.Data),
				MediaType: mediaType,
			},
		}
	case part.URL != "":
		block.Source = anthropic.ImageBlockParamSourceUnion{
			OfURL: &anthropic.URLImageSourceParam{URL: part.URL},
		}
	case part.FileID != "":
		block.Source = anthropic.ImageBlockParamSourceUnion{
			OfFile: &anthropic.FileImageSourceParam{FileID: part.FileID},
		}
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
	return block, nil
}

func imageMediaType(mimeType string) (anthropic.Base64ImageSourceMediaType, error) {
	switch mimeType {
	case "image/jpeg":
		return anthropic.Base64ImageSourceMediaTypeImageJPEG, nil
	case "image/png":
		return anthropic.Base64ImageSourceMediaTypeImagePNG, nil
	case "image/gif":
		return anthropic.Base64ImageSourceMediaTypeImageGIF, nil
	case "image/webp":
		return anthropic.Base64ImageSourceMediaTypeImageWebP, nil
	default:
		return "", fmt.Errorf("anthropic: unsupported image MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
	}
}

func documentSource(part *llm.FilePart, mimeType string) (*anthropic.DocumentBlockParam, error) {
	block := &anthropic.DocumentBlockParam{}
	if part.Filename != "" {
		block.Title = param.NewOpt(part.Filename)
	}
	switch {
	case len(part.Data) > 0:
		if mimeType == "text/plain" {
			block.Source = anthropic.DocumentBlockParamSourceUnion{
				OfText: &anthropic.PlainTextSourceParam{Data: string(part.Data)},
			}
			return block, nil
		}
		block.Source = anthropic.DocumentBlockParamSourceUnion{
			OfBase64: &anthropic.Base64PDFSourceParam{
				Data: base64.StdEncoding.EncodeToString(part.Data),
			},
		}
	case part.URL != "":
		if mimeType != "application/pdf" {
			return nil, fmt.Errorf("%w: Anthropic only accepts URL documents as PDF", llm.ErrUnsupportedPart)
		}
		block.Source = anthropic.DocumentBlockParamSourceUnion{
			OfURL: &anthropic.URLPDFSourceParam{URL: part.URL},
		}
	case part.FileID != "":
		block.Source = anthropic.DocumentBlockParamSourceUnion{
			OfFile: &anthropic.FileDocumentSourceParam{FileID: part.FileID},
		}
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
	return block, nil
}

// normalizeMIME lowercases a media type and drops parameters.
func normalizeMIME(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if mediaType, _, err := mime.ParseMediaType(value); err == nil {
		return strings.ToLower(mediaType)
	}
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}
