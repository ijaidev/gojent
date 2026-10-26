package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/responses"
)

// partState is the per-part replay state the Responses adapter needs. It lives
// in PartMeta.Provider["openai"] and annotates canonical content; it never
// replaces it (D11).
//
// OpenAI returns reasoning summaries but not readable reasoning text, and
// encrypted reasoning plus the provider item ID are required to replay the
// turn. Function call status is stored here because canonical ToolCall.Status
// stays empty; hosted items are recorded verbatim so the client never executes
// them.
type partState struct {
	// ItemID is the provider item identity for reasoning items, whose IDs have
	// no canonical field.
	ItemID string `json:"item_id,omitempty"`
	// EncryptedContent is the opaque reasoning payload OpenAI returns when
	// encrypted reasoning is included.
	EncryptedContent string `json:"encrypted_content,omitempty"`
	// Status is the provider-reported status for reasoning and function items.
	Status string `json:"status,omitempty"`
	// ServerItem is the raw hosted output item recorded on a server call.
	ServerItem json.RawMessage `json:"server_item,omitempty"`
}

// empty reports whether the state carries nothing to replay. RawMessage is not
// comparable, so the struct cannot be compared to its zero value.
func (s partState) empty() bool {
	return s.ItemID == "" && s.EncryptedContent == "" && s.Status == "" && len(s.ServerItem) == 0
}

// stateOf reads OpenAI replay metadata from a canonical part. Missing metadata
// is not an error; malformed metadata fails closed.
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

// attachState stores OpenAI replay metadata on a canonical part.
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

// canonicalResponse converts a non-streaming Responses response into canonical
// form. Hosted tool items are recorded on their server call instead of being
// exposed as separately executable content.
func (m *Model) canonicalResponse(response *responses.Response) (*llm.Response, error) {
	if response == nil {
		return nil, fmt.Errorf("%w: nil response", llm.ErrInvalidMessage)
	}

	status, err := checkTerminalStatus(response)
	if err != nil {
		return nil, err
	}
	parts, err := canonicalPartsFromOutput(response.Output)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("openai: response contains no content: %w", llm.ErrInvalidMessage)
	}

	message := llm.Message{
		Role:    llm.RoleAssistant,
		Content: parts,
		Status:  status,
	}
	if err := message.Validate(); err != nil {
		return nil, err
	}

	modelID := response.Model
	if modelID == "" {
		modelID = m.modelID
	}
	return &llm.Response{
		ID:              response.ID,
		Model:           modelID,
		Message:         message,
		Status:          status,
		FinishReason:    terminalFinishReason(response, hasClientToolCalls(parts)),
		RawFinishReason: rawFinishReason(response),
		Usage:           usageFromResponse(response.Usage),
	}, nil
}

// canonicalPartsFromOutput converts output items in provider order. An empty
// message content part adds no canonical part, matching the streaming path,
// which lets the assembler drop empty parts.
func canonicalPartsFromOutput(items []responses.ResponseOutputItemUnion) ([]llm.Part, error) {
	parts := make([]llm.Part, 0, len(items))
	for index := range items {
		item := &items[index]
		switch item.Type {
		case "message":
			content, err := canonicalMessageContent(item)
			if err != nil {
				return nil, fmt.Errorf("openai: output[%d]: %w", index, err)
			}
			parts = append(parts, content...)

		case "function_call":
			part, err := functionCallPart(item, true)
			if err != nil {
				return nil, fmt.Errorf("openai: output[%d]: %w", index, err)
			}
			parts = append(parts, part)

		case "reasoning":
			part, err := reasoningPart(item)
			if err != nil {
				return nil, fmt.Errorf("openai: output[%d]: %w", index, err)
			}
			parts = append(parts, part)

		default:
			if !isHostedItemType(item.Type) {
				return nil, fmt.Errorf("%w: openai output item %q", llm.ErrUnsupportedPartType, item.Type)
			}
			part, err := hostedToolCallPart(item)
			if err != nil {
				return nil, fmt.Errorf("openai: output[%d]: %w", index, err)
			}
			parts = append(parts, part)
		}
	}
	return parts, nil
}

// canonicalMessageContent converts one output message's content parts. OpenAI
// annotations and logprobs have no canonical representation and are ignored;
// the text itself stays authoritative.
func canonicalMessageContent(item *responses.ResponseOutputItemUnion) ([]llm.Part, error) {
	parts := make([]llm.Part, 0, len(item.Content))
	for index := range item.Content {
		content := &item.Content[index]
		kind, text, err := contentPartInfo(content)
		if err != nil {
			return nil, fmt.Errorf("content[%d]: %w", index, err)
		}
		if text == "" {
			continue
		}
		parts = append(parts, contentPart(kind, text))
	}
	return parts, nil
}

// contentPartInfo reads one output content part's kind and final text.
func contentPartInfo(content *responses.ResponseOutputMessageContentUnion) (llm.PartType, string, error) {
	switch content.Type {
	case "output_text":
		return llm.PartTypeText, content.Text, nil
	case "refusal":
		return llm.PartTypeRefusal, content.Refusal, nil
	default:
		return "", "", fmt.Errorf("%w: openai output content %q", llm.ErrUnsupportedPartType, content.Type)
	}
}

// contentPart builds the canonical part for one output content kind.
func contentPart(kind llm.PartType, text string) llm.Part {
	if kind == llm.PartTypeRefusal {
		return llm.NewRefusalPart(text)
	}
	return llm.NewTextPart(text)
}

// functionCallPart converts one function_call output item. The item ID and
// correlation call ID stay separate (D7). Canonical Status is left empty; the
// provider status is recorded in part metadata so Generate and Stream agree.
func functionCallPart(item *responses.ResponseOutputItemUnion, includeStatus bool) (*llm.ToolCallPart, error) {
	if item.CallID == "" {
		return nil, fmt.Errorf("%w: OpenAI function call has no call id", llm.ErrInvalidToolCall)
	}
	if item.Name == "" {
		return nil, fmt.Errorf("%w: OpenAI function call %q has no name", llm.ErrInvalidToolCall, item.CallID)
	}
	input, err := canonicalArguments(item.Arguments)
	if err != nil {
		return nil, err
	}

	part := llm.NewToolCallPart(llm.ToolCall{
		ItemID: item.ID,
		CallID: item.CallID,
		Name:   item.Name,
		Kind:   llm.ToolKindFunction,
		Input:  input,
	})
	if includeStatus && item.Status != "" {
		if err := validateProviderStatus(item.Status); err != nil {
			return nil, err
		}
		if err := attachState(part, partState{Status: item.Status}); err != nil {
			return nil, err
		}
	}
	return part, nil
}

// reasoningPart converts one reasoning output item. OpenAI exposes summaries,
// not readable reasoning text; the item ID and encrypted content are required
// for replay and live in part metadata.
func reasoningPart(item *responses.ResponseOutputItemUnion) (*llm.ReasoningPart, error) {
	if item.ID == "" {
		return nil, fmt.Errorf("%w: OpenAI reasoning item has no id", llm.ErrUnsupportedPart)
	}
	state := partState{
		ItemID:           item.ID,
		EncryptedContent: item.EncryptedContent,
	}
	if item.Status != "" {
		if err := validateProviderStatus(item.Status); err != nil {
			return nil, err
		}
		state.Status = item.Status
	}

	part := llm.NewReasoningPart("")
	for _, summary := range item.Summary {
		if summary.Text == "" {
			continue
		}
		part.Summary = append(part.Summary, summary.Text)
	}
	if err := attachState(part, state); err != nil {
		return nil, err
	}
	return part, nil
}

// hostedToolCallPart records one provider-executed output item as a server
// tool call. The raw item is stored verbatim and replayed on the next turn;
// the client never executes it.
func hostedToolCallPart(item *responses.ResponseOutputItemUnion) (*llm.ToolCallPart, error) {
	if item.ID == "" {
		return nil, fmt.Errorf("%w: OpenAI %s item has no id", llm.ErrUnsupportedPart, item.Type)
	}
	raw := item.RawJSON()
	if raw == "" {
		return nil, fmt.Errorf("%w: OpenAI %s item has no JSON", llm.ErrUnsupportedPart, item.Type)
	}

	part := llm.NewToolCallPart(llm.ToolCall{
		ItemID: item.ID,
		CallID: item.ID,
		Name:   hostedToolName(item),
		Kind:   llm.ToolKindServer,
		Input:  json.RawMessage("{}"),
	})
	if err := attachState(part, partState{ServerItem: json.RawMessage(raw)}); err != nil {
		return nil, err
	}
	return part, nil
}

// isHostedItemType reports whether a Responses output item is executed by
// OpenAI rather than the client.
func isHostedItemType(itemType string) bool {
	switch itemType {
	case "file_search_call", "web_search_call", "computer_call",
		"image_generation_call", "code_interpreter_call", "local_shell_call",
		"mcp_call", "mcp_list_tools", "mcp_approval_request":
		return true
	default:
		return false
	}
}

// hostedToolName is the stable canonical name for a hosted output item.
func hostedToolName(item *responses.ResponseOutputItemUnion) string {
	switch item.Type {
	case "web_search_call":
		return "web_search"
	case "file_search_call":
		return "file_search"
	case "computer_call":
		return "computer_use"
	case "code_interpreter_call":
		return "code_interpreter"
	case "image_generation_call":
		return "image_generation"
	case "local_shell_call":
		return "local_shell"
	case "mcp_call":
		if item.Name != "" {
			return item.Name
		}
		return "mcp_call"
	default:
		return item.Type
	}
}

// checkTerminalStatus maps the provider response status onto canonical message
// status. Failed, canceled, and non-terminal responses become provider errors
// instead of partial messages.
func checkTerminalStatus(response *responses.Response) (llm.MessageStatus, error) {
	switch response.Status {
	case "", responses.ResponseStatusCompleted:
		return llm.MessageStatusCompleted, nil
	case responses.ResponseStatusIncomplete:
		return llm.MessageStatusIncomplete, nil
	case responses.ResponseStatusFailed:
		return "", responseFailure(response)
	case responses.ResponseStatusCancelled:
		return "", &llm.Error{
			Kind: llm.KindProvider,
			Err:  fmt.Errorf("openai: response %q was canceled", response.ID),
		}
	default:
		return "", &llm.Error{
			Kind: llm.KindProvider,
			Err:  fmt.Errorf("openai: response %q has non-terminal status %q", response.ID, response.Status),
		}
	}
}

// responseFailure turns a failed response into a canonical provider error.
func responseFailure(response *responses.Response) error {
	code := string(response.Error.Code)
	message := strings.TrimSpace(response.Error.Message)
	if message == "" {
		message = "response failed"
	}
	if code != "" {
		return &llm.Error{
			Kind: classifyCode(code),
			Err:  fmt.Errorf("openai: response %q failed (%s): %s", response.ID, code, message),
		}
	}
	return &llm.Error{
		Kind: llm.KindProvider,
		Err:  fmt.Errorf("openai: response %q failed: %s", response.ID, message),
	}
}

// terminalFinishReason derives the canonical reason. Responses has no finish
// reason field, so incomplete reasons drive the value and a completed response
// with unresolved client tool calls is a tool request.
func terminalFinishReason(response *responses.Response, hasClientCalls bool) llm.FinishReason {
	if response.Status == responses.ResponseStatusIncomplete {
		switch reason := response.IncompleteDetails.Reason; reason {
		case "":
			return llm.FinishReasonStop
		case "max_output_tokens":
			return llm.FinishReasonLength
		case "content_filter":
			return llm.FinishReasonContentFilter
		default:
			return llm.FinishReason(strings.ToLower(reason))
		}
	}
	if hasClientCalls {
		return llm.FinishReasonTool
	}
	return llm.FinishReasonStop
}

// rawFinishReason reports the provider's own terminal reason. Responses only
// reports an incomplete reason; otherwise the terminal status is the closest
// provider-supplied value.
func rawFinishReason(response *responses.Response) string {
	if response.Status == responses.ResponseStatusIncomplete && response.IncompleteDetails.Reason != "" {
		return response.IncompleteDetails.Reason
	}
	return string(response.Status)
}

// hasClientToolCalls reports whether the message asks the runner to execute a
// tool. Hosted server calls do not count.
func hasClientToolCalls(parts []llm.Part) bool {
	for _, part := range parts {
		call, ok := part.(*llm.ToolCallPart)
		if ok && call.EffectiveKind() != llm.ToolKindServer {
			return true
		}
	}
	return false
}

// usageFromResponse converts the terminal usage snapshot.
func usageFromResponse(usage responses.ResponseUsage) llm.Usage {
	return llm.Usage{
		PromptTokens:     int(usage.InputTokens),
		CompletionTokens: int(usage.OutputTokens),
		TotalTokens:      int(usage.TotalTokens),
		ReasoningTokens:  int(usage.OutputTokensDetails.ReasoningTokens),
		CachedTokens:     int(usage.InputTokensDetails.CachedTokens),
	}
}
