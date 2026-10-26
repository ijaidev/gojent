package openai

import (
	"context"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/openai/openai-go/responses"
)

// streamContentPart accumulates one canonical message content part. OpenAI
// text and refusal deltas append; the final content part snapshot is
// authoritative and is applied as a Replace before the part ends.
type streamContentPart struct {
	kind      llm.PartType
	index     int
	text      strings.Builder
	finalText string
	ended     bool
}

// streamOutputItem tracks one Responses output item and the canonical part
// indices reserved for it. Reasoning and hosted items are buffered until the
// provider finishes the item because their canonical content cannot be built
// incrementally.
type streamOutputItem struct {
	outputIndex int64
	itemType    string
	index       int
	content     map[int64]*streamContentPart
	args        strings.Builder
	finalArgs   string
	item        responses.ResponseOutputItemUnion
	emitted     bool
	ended       bool
	done        bool
}

// streamState maps Responses stream events onto canonical part-indexed events.
type streamState struct {
	ctx    context.Context
	events chan<- llm.StreamEvent
	model  string

	items     map[int64]*streamOutputItem
	order     []int64
	nextIndex int
	ended     map[int]bool

	messageStarted bool
	terminal       *responses.Response
	terminalSeen   bool
	clientCalls    bool
}

func newStreamState(ctx context.Context, events chan<- llm.StreamEvent, model string) *streamState {
	return &streamState{
		ctx:    ctx,
		events: events,
		model:  model,
		items:  make(map[int64]*streamOutputItem),
		ended:  make(map[int]bool),
	}
}

func (s *streamState) emit(event llm.StreamEvent) error {
	select {
	case s.events <- event:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// apply consumes one SDK stream event. Terminal responses are stored and
// reconciled in finish so a clean SDK end and the terminal event share one
// code path.
func (s *streamState) apply(event responses.ResponseStreamEventUnion) error {
	if s.terminalSeen {
		return fmt.Errorf("%w: openai stream event %q after the terminal response", llm.ErrInvalidMessage, event.Type)
	}

	switch event.Type {
	case "response.created", "response.in_progress", "response.queued":
		return s.startMessage(event.Response)

	case "response.output_item.added":
		return s.addOutputItem(event.OutputIndex, event.Item)

	case "response.output_item.done":
		return s.doneOutputItem(event.OutputIndex, event.Item)

	case "response.content_part.added":
		return s.addContentPart(event.OutputIndex, event.ContentIndex, &event.Part)

	case "response.content_part.done":
		return s.doneContentPart(event.OutputIndex, event.ContentIndex, &event.Part)

	case "response.output_text.delta":
		return s.contentDelta(event.OutputIndex, event.ContentIndex, llm.PartTypeText, event.Delta.OfString)

	case "response.output_text.done":
		return s.finalContentText(event.OutputIndex, event.ContentIndex, llm.PartTypeText, event.Text)

	case "response.refusal.delta":
		return s.contentDelta(event.OutputIndex, event.ContentIndex, llm.PartTypeRefusal, event.Delta.OfString)

	case "response.refusal.done":
		return s.finalContentText(event.OutputIndex, event.ContentIndex, llm.PartTypeRefusal, event.Refusal)

	case "response.function_call_arguments.delta":
		return s.functionArgsDelta(event.OutputIndex, event.Delta.OfString)

	case "response.function_call_arguments.done":
		return s.functionArgsDone(event.OutputIndex, event.Arguments)

	case "response.completed", "response.incomplete":
		response := event.Response
		s.terminal = &response
		s.terminalSeen = true
		return s.startMessage(response)

	case "response.failed":
		return responseFailure(&event.Response)

	case "error":
		return streamError(event.Code, event.Message)

	case "response.output_text.annotation.added",
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.reasoning_summary.delta",
		"response.reasoning_summary.done",
		"response.web_search_call.in_progress",
		"response.web_search_call.searching",
		"response.web_search_call.completed",
		"response.file_search_call.in_progress",
		"response.file_search_call.searching",
		"response.file_search_call.completed",
		"response.code_interpreter_call.in_progress",
		"response.code_interpreter_call.interpreting",
		"response.code_interpreter_call.completed",
		"response.code_interpreter_call_code.delta",
		"response.code_interpreter_call_code.done",
		"response.image_generation_call.in_progress",
		"response.image_generation_call.generating",
		"response.image_generation_call.completed",
		"response.image_generation_call.partial_image",
		"response.mcp_call.in_progress",
		"response.mcp_call.completed",
		"response.mcp_call.failed",
		"response.mcp_call_arguments.delta",
		"response.mcp_call_arguments.done",
		"response.mcp_list_tools.in_progress",
		"response.mcp_list_tools.completed",
		"response.mcp_list_tools.failed":
		// These events carry provider progress for hosted tools or annotations
		// with no canonical representation. The final output item is recorded
		// verbatim when it arrives.
		return nil

	case "response.audio.delta", "response.audio.done",
		"response.audio.transcript.delta", "response.audio.transcript.done":
		return fmt.Errorf("%w: openai audio output", llm.ErrUnsupported)

	default:
		return fmt.Errorf("%w: openai stream event %q", llm.ErrUnsupported, event.Type)
	}
}

func (s *streamState) startMessage(response responses.Response) error {
	if s.messageStarted {
		return nil
	}
	s.messageStarted = true
	modelID := response.Model
	if modelID == "" {
		modelID = s.model
	}
	return s.emit(llm.MessageStart{ID: response.ID, Model: modelID})
}

// addOutputItem reserves canonical indices in output order. Function calls
// stream immediately; reasoning and hosted items wait for their final item.
func (s *streamState) addOutputItem(outputIndex int64, item responses.ResponseOutputItemUnion) error {
	if _, exists := s.items[outputIndex]; exists {
		return fmt.Errorf("%w: duplicate output item %d", llm.ErrInvalidPart, outputIndex)
	}

	state := &streamOutputItem{
		outputIndex: outputIndex,
		itemType:    item.Type,
		index:       -1,
		content:     make(map[int64]*streamContentPart),
		item:        item,
	}

	switch item.Type {
	case "message":
		// Message items produce one canonical part per content part.

	case "function_call":
		part, err := functionCallPart(&item, false)
		if err != nil {
			return err
		}
		state.index = s.reservePart()
		s.clientCalls = true
		if err := s.emit(llm.PartStart{Index: state.index, Part: part}); err != nil {
			return err
		}

	case "reasoning":
		if item.ID == "" {
			return fmt.Errorf("%w: OpenAI reasoning item has no id", llm.ErrUnsupportedPart)
		}
		state.index = s.reservePart()

	default:
		if !isHostedItemType(item.Type) {
			return fmt.Errorf("%w: openai output item %q", llm.ErrUnsupportedPartType, item.Type)
		}
		if item.ID == "" {
			return fmt.Errorf("%w: OpenAI %s item has no id", llm.ErrUnsupportedPart, item.Type)
		}
		state.index = s.reservePart()
	}

	s.items[outputIndex] = state
	s.order = append(s.order, outputIndex)
	return nil
}

func (s *streamState) doneOutputItem(outputIndex int64, item responses.ResponseOutputItemUnion) error {
	state, exists := s.items[outputIndex]
	if !exists {
		if err := s.addOutputItem(outputIndex, item); err != nil {
			return err
		}
		state = s.items[outputIndex]
	}
	if state.done {
		return fmt.Errorf("%w: duplicate output item done %d", llm.ErrInvalidPart, outputIndex)
	}
	if state.itemType != item.Type {
		return fmt.Errorf("%w: output item %d changed type from %q to %q", llm.ErrInvalidPart, outputIndex, state.itemType, item.Type)
	}
	state.done = true
	state.item = item

	switch item.Type {
	case "message":
		return s.finalizeMessageItem(state, &item)
	case "function_call":
		return s.updateFunctionCall(state, &item)
	case "reasoning":
		return s.emitReasoning(state, &item)
	default:
		if !isHostedItemType(item.Type) {
			return fmt.Errorf("%w: openai output item %q", llm.ErrUnsupportedPartType, item.Type)
		}
		return s.emitHosted(state, &item)
	}
}

// addContentPart reserves a canonical index for one message content part. Empty
// parts are still started so later indices stay in output order; the assembler
// drops them when the stream finishes, matching the non-streaming path.
func (s *streamState) addContentPart(outputIndex, contentIndex int64, part *responses.ResponseStreamEventUnionPart) error {
	item, err := s.messageItem(outputIndex)
	if err != nil {
		return err
	}
	if _, exists := item.content[contentIndex]; exists {
		return fmt.Errorf("%w: duplicate content part %d for output item %d", llm.ErrInvalidPart, contentIndex, outputIndex)
	}
	kind, _, err := streamContentPartInfo(part)
	if err != nil {
		return err
	}
	state := &streamContentPart{kind: kind, index: s.reservePart()}
	item.content[contentIndex] = state
	return s.emit(llm.PartStart{Index: state.index, Part: contentPart(kind, "")})
}

func (s *streamState) doneContentPart(outputIndex, contentIndex int64, part *responses.ResponseStreamEventUnionPart) error {
	kind, text, err := streamContentPartInfo(part)
	if err != nil {
		return err
	}
	state, err := s.contentState(outputIndex, contentIndex, kind)
	if err != nil {
		return err
	}
	return s.finalizeContent(state, text)
}

func (s *streamState) contentDelta(outputIndex, contentIndex int64, kind llm.PartType, delta string) error {
	if delta == "" {
		return nil
	}
	state, err := s.contentState(outputIndex, contentIndex, kind)
	if err != nil {
		return err
	}
	if state.ended {
		return fmt.Errorf("%w: delta after content part %d ended", llm.ErrInvalidPart, contentIndex)
	}
	state.text.WriteString(delta)
	return s.emit(llm.PartDelta{Index: state.index, Kind: kind, Text: delta})
}

func (s *streamState) finalContentText(outputIndex, contentIndex int64, kind llm.PartType, text string) error {
	if text == "" {
		return nil
	}
	state, err := s.contentState(outputIndex, contentIndex, kind)
	if err != nil {
		return err
	}
	state.finalText = text
	return nil
}

func (s *streamState) contentState(outputIndex, contentIndex int64, kind llm.PartType) (*streamContentPart, error) {
	item, err := s.messageItem(outputIndex)
	if err != nil {
		return nil, err
	}
	state := item.content[contentIndex]
	if state == nil {
		state = &streamContentPart{kind: kind, index: s.reservePart()}
		item.content[contentIndex] = state
		if err := s.emit(llm.PartStart{Index: state.index, Part: contentPart(kind, "")}); err != nil {
			return nil, err
		}
		return state, nil
	}
	if state.kind != kind {
		return nil, fmt.Errorf("%w: content part %d is %q, not %q", llm.ErrInvalidPart, contentIndex, state.kind, kind)
	}
	return state, nil
}

// messageItem returns the state for a message output item, creating one when a
// content event arrives before the output item event.
func (s *streamState) messageItem(outputIndex int64) (*streamOutputItem, error) {
	state := s.items[outputIndex]
	if state == nil {
		state = &streamOutputItem{
			outputIndex: outputIndex,
			itemType:    "message",
			index:       -1,
			content:     make(map[int64]*streamContentPart),
		}
		s.items[outputIndex] = state
		s.order = append(s.order, outputIndex)
		return state, nil
	}
	if state.itemType != "message" {
		return nil, fmt.Errorf("%w: output item %d is %q, not a message", llm.ErrInvalidPart, outputIndex, state.itemType)
	}
	return state, nil
}

func (s *streamState) finalizeMessageItem(state *streamOutputItem, item *responses.ResponseOutputItemUnion) error {
	seen := make(map[int64]bool, len(item.Content))
	for index := range item.Content {
		contentIndex := int64(index)
		kind, text, err := contentPartInfo(&item.Content[index])
		if err != nil {
			return err
		}
		part := state.content[contentIndex]
		switch {
		case part == nil:
			part = &streamContentPart{kind: kind, index: s.reservePart()}
			state.content[contentIndex] = part
			if err := s.emit(llm.PartStart{Index: part.index, Part: contentPart(kind, text)}); err != nil {
				return err
			}
			part.text.WriteString(text)
			part.finalText = text
		case part.kind != kind:
			return fmt.Errorf("%w: content part %d is %q, not %q", llm.ErrInvalidPart, contentIndex, part.kind, kind)
		case text != "":
			part.finalText = text
		}
		seen[contentIndex] = true
		if err := s.finalizeContent(part, text); err != nil {
			return err
		}
	}

	for contentIndex, part := range state.content {
		if seen[contentIndex] {
			continue
		}
		if err := s.finalizeContent(part, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *streamState) finalizeContent(part *streamContentPart, text string) error {
	if part.ended {
		return nil
	}
	final := text
	if final == "" {
		final = part.finalText
	}
	current := part.text.String()
	if final != "" && final != current {
		if err := s.emit(llm.PartDelta{Index: part.index, Kind: part.kind, Replace: true, Text: final}); err != nil {
			return err
		}
	}
	part.ended = true
	return s.endPart(part.index)
}

func (s *streamState) functionArgsDelta(outputIndex int64, delta string) error {
	state, err := s.functionItem(outputIndex)
	if err != nil {
		return err
	}
	if delta == "" {
		return nil
	}
	state.args.WriteString(delta)
	return s.emit(llm.PartDelta{
		Index:   state.index,
		Kind:    llm.PartTypeToolCall,
		Replace: true,
		Call: &llm.ToolCallDelta{
			ItemID:        state.item.ID,
			CallID:        state.item.CallID,
			Name:          state.item.Name,
			InputFragment: state.args.String(),
		},
	})
}

func (s *streamState) functionArgsDone(outputIndex int64, arguments string) error {
	state, err := s.functionItem(outputIndex)
	if err != nil {
		return err
	}
	state.finalArgs = arguments
	return nil
}

func (s *streamState) functionItem(outputIndex int64) (*streamOutputItem, error) {
	state, exists := s.items[outputIndex]
	if !exists {
		return nil, fmt.Errorf("%w: function arguments for unknown output item %d", llm.ErrInvalidPart, outputIndex)
	}
	if state.itemType != "function_call" {
		return nil, fmt.Errorf("%w: output item %d is %q, not a function call", llm.ErrInvalidPart, outputIndex, state.itemType)
	}
	return state, nil
}

// updateFunctionCall applies the authoritative arguments and provider item
// identity without closing the part; the terminal response supplies the final
// provider status.
func (s *streamState) updateFunctionCall(state *streamOutputItem, item *responses.ResponseOutputItemUnion) error {
	if state.index < 0 {
		part, err := functionCallPart(item, false)
		if err != nil {
			return err
		}
		state.index = s.reservePart()
		s.clientCalls = true
		if err := s.emit(llm.PartStart{Index: state.index, Part: part}); err != nil {
			return err
		}
	}

	input, err := canonicalArguments(finalArguments(state, item))
	if err != nil {
		return err
	}
	return s.emit(llm.PartDelta{
		Index:   state.index,
		Kind:    llm.PartTypeToolCall,
		Replace: true,
		Call: &llm.ToolCallDelta{
			ItemID:        item.ID,
			CallID:        item.CallID,
			Name:          item.Name,
			InputSnapshot: input,
		},
	})
}

// finalizeFunctionCall closes a function call with the terminal item's status.
func (s *streamState) finalizeFunctionCall(state *streamOutputItem, item *responses.ResponseOutputItemUnion) error {
	if err := s.updateFunctionCall(state, item); err != nil {
		return err
	}
	if state.ended {
		return nil
	}

	provider := partState{}
	if item.Status != "" {
		if err := validateProviderStatus(item.Status); err != nil {
			return err
		}
		provider.Status = item.Status
	}
	if data := providerDataFor(provider); len(data) > 0 {
		if err := s.emit(llm.PartDelta{
			Index: state.index,
			Kind:  llm.PartTypeToolCall,
			Call: &llm.ToolCallDelta{
				ItemID: item.ID,
				CallID: item.CallID,
				Name:   item.Name,
			},
			Provider: data,
		}); err != nil {
			return err
		}
	}
	state.ended = true
	return s.endPart(state.index)
}

func finalArguments(state *streamOutputItem, item *responses.ResponseOutputItemUnion) string {
	if strings.TrimSpace(item.Arguments) != "" {
		return item.Arguments
	}
	if strings.TrimSpace(state.finalArgs) != "" {
		return state.finalArgs
	}
	return state.args.String()
}

func (s *streamState) emitReasoning(state *streamOutputItem, item *responses.ResponseOutputItemUnion) error {
	if state.emitted {
		return nil
	}
	part, err := reasoningPart(item)
	if err != nil {
		return err
	}
	if state.index < 0 {
		state.index = s.reservePart()
	}
	state.emitted = true
	if err := s.emit(llm.PartStart{Index: state.index, Part: part}); err != nil {
		return err
	}
	state.ended = true
	return s.endPart(state.index)
}

func (s *streamState) emitHosted(state *streamOutputItem, item *responses.ResponseOutputItemUnion) error {
	if state.emitted {
		return nil
	}
	part, err := hostedToolCallPart(item)
	if err != nil {
		return err
	}
	if state.index < 0 {
		state.index = s.reservePart()
	}
	state.emitted = true
	if err := s.emit(llm.PartStart{Index: state.index, Part: part}); err != nil {
		return err
	}
	state.ended = true
	return s.endPart(state.index)
}

// finish reconciles the terminal response with streamed items and emits the
// terminal message event.
func (s *streamState) finish() error {
	if s.terminal == nil {
		return fmt.Errorf("%w: openai stream produced no terminal response", llm.ErrInvalidMessage)
	}
	if !s.messageStarted {
		if err := s.startMessage(*s.terminal); err != nil {
			return err
		}
	}
	if err := s.reconcileTerminal(s.terminal); err != nil {
		return err
	}
	if s.nextIndex == 0 {
		return fmt.Errorf("openai: response contains no content: %w", llm.ErrInvalidMessage)
	}

	for index := 0; index < s.nextIndex; index++ {
		if !s.ended[index] {
			if err := s.endPart(index); err != nil {
				return err
			}
		}
	}

	status, err := checkTerminalStatus(s.terminal)
	if err != nil {
		return err
	}
	return s.emit(llm.MessageEnd{
		Status:          status,
		FinishReason:    terminalFinishReason(s.terminal, s.clientCalls),
		RawFinishReason: rawFinishReason(s.terminal),
		Usage:           usageFromResponse(s.terminal.Usage),
	})
}

func (s *streamState) reconcileTerminal(response *responses.Response) error {
	seen := make(map[int64]bool, len(response.Output))
	for index := range response.Output {
		outputIndex := int64(index)
		item := &response.Output[index]
		seen[outputIndex] = true

		state, exists := s.items[outputIndex]
		if !exists {
			if err := s.addOutputItem(outputIndex, *item); err != nil {
				return err
			}
			state = s.items[outputIndex]
		}
		state.item = *item

		switch item.Type {
		case "message":
			if err := s.finalizeMessageItem(state, item); err != nil {
				return err
			}
		case "function_call":
			if err := s.finalizeFunctionCall(state, item); err != nil {
				return err
			}
		case "reasoning":
			if err := s.emitReasoning(state, item); err != nil {
				return err
			}
		default:
			if !isHostedItemType(item.Type) {
				return fmt.Errorf("%w: openai output item %q", llm.ErrUnsupportedPartType, item.Type)
			}
			if err := s.emitHosted(state, item); err != nil {
				return err
			}
		}
	}

	for _, outputIndex := range s.order {
		if seen[outputIndex] {
			continue
		}
		state := s.items[outputIndex]
		if state == nil {
			continue
		}
		item := &state.item
		switch state.itemType {
		case "message":
			if err := s.finalizeMessageItem(state, item); err != nil {
				return err
			}
		case "function_call":
			if err := s.finalizeFunctionCall(state, item); err != nil {
				return err
			}
		case "reasoning":
			if err := s.emitReasoning(state, item); err != nil {
				return err
			}
		default:
			if isHostedItemType(state.itemType) {
				if err := s.emitHosted(state, item); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *streamState) reservePart() int {
	index := s.nextIndex
	s.nextIndex++
	return index
}

func (s *streamState) endPart(index int) error {
	if s.ended[index] {
		return nil
	}
	s.ended[index] = true
	return s.emit(llm.PartEnd{Index: index})
}

// streamContentPartInfo reads the kind and final text of one streamed content
// part.
func streamContentPartInfo(part *responses.ResponseStreamEventUnionPart) (llm.PartType, string, error) {
	switch part.Type {
	case "output_text":
		return llm.PartTypeText, part.Text, nil
	case "refusal":
		return llm.PartTypeRefusal, part.Refusal, nil
	default:
		return "", "", fmt.Errorf("%w: openai output content %q", llm.ErrUnsupportedPartType, part.Type)
	}
}

// sendStreamError reports a terminal error unless the consumer went away.
func sendStreamError(ctx context.Context, ch chan<- error, err error) {
	select {
	case ch <- err:
	case <-ctx.Done():
	}
}
