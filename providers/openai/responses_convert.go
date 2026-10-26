package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared/constant"
)

// convertInput translates canonical user and assistant messages into Responses
// input items. System and developer messages are hoisted into the top-level
// instructions string by convertInstructions and are skipped here.
//
// Parts stay in canonical order. A message item is flushed whenever a tool
// call, tool result, or reasoning item interrupts the text stream, because
// those items are top-level Responses items rather than message content.
func convertInput(messages []llm.Message) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam

	for index := range messages {
		message := &messages[index]
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			continue
		case llm.RoleUser:
			converted, err := convertUserMessage(message)
			if err != nil {
				return nil, fmt.Errorf("openai: message %d: %w", index, err)
			}
			items = append(items, converted...)
		case llm.RoleAssistant:
			converted, err := convertAssistantMessage(message)
			if err != nil {
				return nil, fmt.Errorf("openai: message %d: %w", index, err)
			}
			items = append(items, converted...)
		default:
			return nil, fmt.Errorf("%w: role %q", llm.ErrInvalidMessage, message.Role)
		}
	}
	return items, nil
}

// convertUserMessage turns one user turn into message items and top-level
// function_call_output items. Tool results are not message content in the
// Responses API, so a mixed user turn may produce several items.
func convertUserMessage(message *llm.Message) (responses.ResponseInputParam, error) {
	if message.Name != "" || message.Phase != "" {
		return nil, fmt.Errorf("%w: openai cannot represent message name or phase", llm.ErrUnsupported)
	}

	var items responses.ResponseInputParam
	var content responses.ResponseInputMessageContentListParam
	flush := func() {
		if len(content) == 0 {
			return
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser))
		content = nil
	}

	for index, part := range message.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			content = append(content, inputTextContent(p.Text))
		case *llm.FilePart:
			converted, err := inputFileContent(p)
			if err != nil {
				return nil, fmt.Errorf("user content[%d]: %w", index, err)
			}
			content = append(content, converted)
		case *llm.ToolResultPart:
			flush()
			converted, err := inputToolResult(&p.ToolResult)
			if err != nil {
				return nil, fmt.Errorf("user content[%d]: %w", index, err)
			}
			items = append(items, converted)
		default:
			return nil, fmt.Errorf("%w: user content[%d] is %T", llm.ErrUnsupportedPartType, index, part)
		}
	}
	flush()
	return items, nil
}

// convertAssistantMessage turns one assistant turn into message items,
// reasoning items, function calls, and recorded hosted tool items.
func convertAssistantMessage(message *llm.Message) (responses.ResponseInputParam, error) {
	if message.Name != "" || message.Phase != "" {
		return nil, fmt.Errorf("%w: openai cannot represent message name or phase", llm.ErrUnsupported)
	}

	var items responses.ResponseInputParam
	var content responses.ResponseInputMessageContentListParam
	flush := func() {
		if len(content) == 0 {
			return
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleAssistant))
		content = nil
	}

	for index, part := range message.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			content = append(content, inputTextContent(p.Text))
		case *llm.RefusalPart:
			// Responses input messages only accept text, image, and file
			// content. A recorded refusal is model-authored text on replay.
			content = append(content, inputTextContent(p.Text))
		case *llm.ReasoningPart:
			flush()
			item, err := inputReasoning(p)
			if err != nil {
				return nil, fmt.Errorf("assistant content[%d]: %w", index, err)
			}
			items = append(items, item)
		case *llm.ToolCallPart:
			flush()
			item, err := inputToolCall(p)
			if err != nil {
				return nil, fmt.Errorf("assistant content[%d]: %w", index, err)
			}
			items = append(items, item)
		default:
			return nil, fmt.Errorf("%w: assistant content[%d] is %T", llm.ErrUnsupportedPartType, index, part)
		}
	}
	flush()
	return items, nil
}

func inputTextContent(text string) responses.ResponseInputContentUnionParam {
	return responses.ResponseInputContentUnionParam{
		OfInputText: &responses.ResponseInputTextParam{
			Text: text,
			Type: constant.ValueOf[constant.InputText](),
		},
	}
}

// inputFileContent maps one canonical file onto input_image or input_file.
// MIMEType is authoritative (D2, D3).
func inputFileContent(part *llm.FilePart) (responses.ResponseInputContentUnionParam, error) {
	mimeType := normalizeOpenAIMIME(part.MIMEType)
	if mimeType == "" {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("%w: file part is missing a MIME type", llm.ErrInvalidPart)
	}

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		image, err := inputImageContent(part, mimeType)
		if err != nil {
			return responses.ResponseInputContentUnionParam{}, err
		}
		return responses.ResponseInputContentUnionParam{OfInputImage: image}, nil

	case mimeType == "application/pdf" || strings.HasPrefix(mimeType, "text/"):
		file, err := inputFilePart(part, mimeType)
		if err != nil {
			return responses.ResponseInputContentUnionParam{}, err
		}
		return responses.ResponseInputContentUnionParam{OfInputFile: file}, nil

	case strings.HasPrefix(mimeType, "audio/"):
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not accept audio input", llm.ErrUnsupportedPartType)
	case strings.HasPrefix(mimeType, "video/"):
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not accept video input", llm.ErrUnsupportedPartType)
	default:
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf(
			"openai: unsupported document MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
	}
}

func inputImageContent(part *llm.FilePart, mimeType string) (*responses.ResponseInputImageParam, error) {
	detail := strings.ToLower(strings.TrimSpace(part.Detail))
	if detail == "" {
		detail = string(responses.ResponseInputImageDetailAuto)
	}
	switch responses.ResponseInputImageDetail(detail) {
	case responses.ResponseInputImageDetailLow, responses.ResponseInputImageDetailHigh, responses.ResponseInputImageDetailAuto:
	default:
		return nil, fmt.Errorf("%w: image detail %q", llm.ErrUnsupported, part.Detail)
	}

	image := &responses.ResponseInputImageParam{
		Detail: responses.ResponseInputImageDetail(detail),
		Type:   constant.ValueOf[constant.InputImage](),
	}
	switch {
	case len(part.Data) > 0:
		image.ImageURL = param.NewOpt("data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Data))
	case part.URL != "":
		image.ImageURL = param.NewOpt(part.URL)
	case part.FileID != "":
		image.FileID = param.NewOpt(part.FileID)
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
	return image, nil
}

func inputFilePart(part *llm.FilePart, mimeType string) (*responses.ResponseInputFileParam, error) {
	file := &responses.ResponseInputFileParam{Type: constant.ValueOf[constant.InputFile]()}
	if part.Filename != "" {
		file.Filename = param.NewOpt(part.Filename)
	}
	switch {
	case len(part.Data) > 0:
		file.FileData = param.NewOpt("data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Data))
	case part.URL != "":
		file.FileURL = param.NewOpt(part.URL)
	case part.FileID != "":
		file.FileID = param.NewOpt(part.FileID)
	default:
		return nil, fmt.Errorf("%w: file part has no data, url, or file_id", llm.ErrInvalidPart)
	}
	return file, nil
}

// inputReasoning rebuilds a reasoning item. OpenAI accepts summary text plus
// encrypted reasoning, not free-form reasoning text, and requires the provider
// item ID for replay.
func inputReasoning(part *llm.ReasoningPart) (responses.ResponseInputItemUnionParam, error) {
	if part.Text != "" {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: OpenAI reasoning items cannot carry free-form reasoning text", llm.ErrUnsupportedPart)
	}
	state, err := stateOf(part)
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}
	if state.ItemID == "" {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: reasoning part has no OpenAI reasoning item id", llm.ErrUnsupportedPart)
	}
	if err := validateProviderStatus(state.Status); err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}

	summary := make([]responses.ResponseReasoningItemSummaryParam, 0, len(part.Summary))
	for _, text := range part.Summary {
		if text == "" {
			continue
		}
		summary = append(summary, responses.ResponseReasoningItemSummaryParam{
			Text: text,
			Type: constant.ValueOf[constant.SummaryText](),
		})
	}

	item := &responses.ResponseReasoningItemParam{
		ID:      state.ItemID,
		Summary: summary,
		Type:    constant.ValueOf[constant.Reasoning](),
	}
	if state.EncryptedContent != "" {
		item.EncryptedContent = param.NewOpt(state.EncryptedContent)
	}
	if state.Status != "" {
		item.Status = responses.ResponseReasoningItemStatus(state.Status)
	}
	return responses.ResponseInputItemUnionParam{OfReasoning: item}, nil
}

// inputToolCall rebuilds a client function call or a recorded hosted item.
func inputToolCall(part *llm.ToolCallPart) (responses.ResponseInputItemUnionParam, error) {
	call := &part.ToolCall
	if call.CallID == "" {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: tool call %q has no call id", llm.ErrInvalidToolCall, call.Name)
	}
	if call.Name == "" {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: tool call %q has no name", llm.ErrInvalidToolCall, call.CallID)
	}

	switch call.EffectiveKind() {
	case llm.ToolKindFunction, llm.ToolKindMCP:
		return inputFunctionCall(part)
	case llm.ToolKindServer:
		return inputServerCall(part)
	default:
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not support %q tool calls", llm.ErrUnsupported, call.EffectiveKind())
	}
}

func inputFunctionCall(part *llm.ToolCallPart) (responses.ResponseInputItemUnionParam, error) {
	call := &part.ToolCall
	state, err := stateOf(part)
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}
	if err := validateProviderStatus(state.Status); err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}

	arguments, err := canonicalArguments(string(call.Input))
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}
	item := &responses.ResponseFunctionToolCallParam{
		Arguments: string(arguments),
		CallID:    call.CallID,
		Name:      call.Name,
		Type:      constant.ValueOf[constant.FunctionCall](),
	}
	itemID := call.ItemID
	if itemID == "" {
		itemID = state.ItemID
	}
	if itemID != "" {
		item.ID = param.NewOpt(itemID)
	}
	switch {
	case state.Status != "":
		item.Status = responses.ResponseFunctionToolCallStatus(state.Status)
	case call.Status != "":
		status, err := functionCallStatus(call.Status)
		if err != nil {
			return responses.ResponseInputItemUnionParam{}, err
		}
		item.Status = status
	}
	return responses.ResponseInputItemUnionParam{OfFunctionCall: item}, nil
}

// inputServerCall replays the exact hosted output item recorded on the server
// call. The client never executes these calls.
func inputServerCall(part *llm.ToolCallPart) (responses.ResponseInputItemUnionParam, error) {
	state, err := stateOf(part)
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}
	if len(state.ServerItem) == 0 {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: server tool call %q has no recorded OpenAI item", llm.ErrUnsupportedPart, part.Name)
	}
	var item responses.ResponseInputItemUnionParam
	if err := json.Unmarshal(state.ServerItem, &item); err != nil {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"openai: decode recorded server item for %q: %w", part.Name, err)
	}
	if !isHostedInputItem(item) {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: recorded server item for %q", llm.ErrUnsupportedPart, part.Name)
	}
	return item, nil
}

// inputToolResult builds a function_call_output item. The Responses API takes
// a single string output, so file content is unsupported and structured output
// is appended as JSON text.
func inputToolResult(result *llm.ToolResult) (responses.ResponseInputItemUnionParam, error) {
	if result.CallID == "" {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf("%w: tool result has no call id", llm.ErrInvalidToolResult)
	}
	switch result.EffectiveKind() {
	case llm.ToolKindFunction, llm.ToolKindMCP:
	default:
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not support %q tool results", llm.ErrUnsupported, result.EffectiveKind())
	}
	if len(result.InputRequests) > 0 || result.Status == llm.ToolStatusInputRequired {
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not support tool input requests", llm.ErrUnsupported)
	}

	output, err := toolResultOutput(result)
	if err != nil {
		return responses.ResponseInputItemUnionParam{}, err
	}
	item := &responses.ResponseInputItemFunctionCallOutputParam{
		CallID: result.CallID,
		Output: output,
		Type:   constant.ValueOf[constant.FunctionCallOutput](),
	}
	switch result.Status {
	case "", llm.ToolStatusCompleted, llm.ToolStatusError, llm.ToolStatusDenied:
		// The Responses API has no error flag; failure details stay in Output.
	case llm.ToolStatusInProgress:
		item.Status = string(llm.ToolStatusInProgress)
	case llm.ToolStatusIncomplete:
		item.Status = string(llm.ToolStatusIncomplete)
	default:
		return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
			"%w: OpenAI Responses does not support tool result status %q", llm.ErrUnsupported, result.Status)
	}
	return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: item}, nil
}

func toolResultOutput(result *llm.ToolResult) (string, error) {
	blocks := make([]string, 0, len(result.Content)+1)
	for index, part := range result.Content {
		switch p := part.(type) {
		case *llm.TextPart:
			blocks = append(blocks, p.Text)
		case *llm.FilePart:
			return "", fmt.Errorf(
				"%w: OpenAI function call outputs cannot carry files", llm.ErrUnsupportedPartType)
		default:
			return "", fmt.Errorf("%w: content[%d] is %T", llm.ErrInvalidToolResult, index, part)
		}
	}
	if len(result.Structured) > 0 {
		blocks = append(blocks, string(result.Structured))
	}
	return strings.Join(blocks, "\n"), nil
}

// canonicalArguments normalizes a Responses arguments string into canonical
// raw JSON. An absent value is an empty object.
func canonicalArguments(arguments string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage("{}"), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("%w: OpenAI tool arguments are not valid JSON", llm.ErrInvalidToolCall)
	}
	return json.RawMessage(trimmed), nil
}

// validateProviderStatus checks a stored provider status before replaying it.
func validateProviderStatus(status string) error {
	switch status {
	case "", string(llm.ToolStatusInProgress), string(llm.ToolStatusCompleted), string(llm.ToolStatusIncomplete):
		return nil
	default:
		return fmt.Errorf("%w: OpenAI provider status %q", llm.ErrUnsupported, status)
	}
}

func functionCallStatus(status llm.ToolStatus) (responses.ResponseFunctionToolCallStatus, error) {
	switch status {
	case llm.ToolStatusInProgress:
		return responses.ResponseFunctionToolCallStatusInProgress, nil
	case llm.ToolStatusCompleted:
		return responses.ResponseFunctionToolCallStatusCompleted, nil
	case llm.ToolStatusIncomplete:
		return responses.ResponseFunctionToolCallStatusIncomplete, nil
	default:
		return "", fmt.Errorf("%w: OpenAI cannot represent call status %q", llm.ErrUnsupported, status)
	}
}

// isHostedInputItem reports whether a decoded union holds a hosted output
// item. Client outputs and message items are deliberately excluded.
func isHostedInputItem(item responses.ResponseInputItemUnionParam) bool {
	return item.OfFileSearchCall != nil ||
		item.OfComputerCall != nil ||
		item.OfWebSearchCall != nil ||
		item.OfImageGenerationCall != nil ||
		item.OfCodeInterpreterCall != nil ||
		item.OfLocalShellCall != nil ||
		item.OfMcpListTools != nil ||
		item.OfMcpApprovalRequest != nil ||
		item.OfMcpCall != nil
}

// normalizeOpenAIMIME lowercases a media type and drops parameters.
func normalizeOpenAIMIME(value string) string {
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
