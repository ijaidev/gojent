package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ijaidev/gojent/core/llm"
)

// streamPartState is the accumulator for one canonical part. Anthropic block
// indices are mapped onto canonical part indices because hosted result blocks
// add no canonical part.
type streamPartState struct {
	kind       llm.PartType
	callKind   llm.ToolKind
	callID     string
	name       string
	signature  string
	redacted   bool
	input      strings.Builder
	seeded     bool
	hasResult  bool
	serverData json.RawMessage
}

// streamState maps Messages stream events onto canonical part-indexed events.
type streamState struct {
	ctx    context.Context
	events chan<- llm.StreamEvent
	model  string

	byBlock   map[int64]int
	parts     map[int]*streamPartState
	nextIndex int
	ended     map[int]bool

	messageStarted bool
	stopReason     anthropic.StopReason
	stopDetails    anthropic.RefusalStopDetails
	usage          llm.Usage
}

func newStreamState(ctx context.Context, events chan<- llm.StreamEvent, model string) *streamState {
	return &streamState{
		ctx:     ctx,
		events:  events,
		model:   model,
		byBlock: make(map[int64]int),
		parts:   make(map[int]*streamPartState),
		ended:   make(map[int]bool),
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

// apply consumes one SDK stream event.
func (s *streamState) apply(event anthropic.MessageStreamEventUnion) error {
	switch event.Type {
	case "message_start":
		return s.startMessage(event.Message)

	case "content_block_start":
		return s.startBlock(event.Index, event.ContentBlock)

	case "content_block_delta":
		return s.applyDelta(event.Index, event.Delta)

	case "content_block_stop":
		return s.stopBlock(event.Index)

	case "message_delta":
		s.stopReason = event.Delta.StopReason
		s.stopDetails = event.Delta.StopDetails
		applyDeltaUsage(&s.usage, event.Usage)
		return nil

	case "message_stop":
		return nil

	default:
		return fmt.Errorf("%w: anthropic stream event %q", llm.ErrUnsupported, event.Type)
	}
}

func (s *streamState) startMessage(message anthropic.Message) error {
	if s.messageStarted {
		return fmt.Errorf("%w: duplicate message_start", llm.ErrInvalidMessage)
	}
	s.messageStarted = true
	s.usage = usageFromMessage(message.Usage)

	modelID := message.Model
	if modelID == "" {
		modelID = s.model
	}
	return s.emit(llm.MessageStart{ID: message.ID, Model: modelID})
}

func (s *streamState) startBlock(index int64, block anthropic.ContentBlockStartEventContentBlockUnion) error {
	switch block.Type {
	case "text":
		partIndex, err := s.addPart(index)
		if err != nil {
			return err
		}
		s.parts[partIndex] = &streamPartState{kind: llm.PartTypeText}
		return s.emit(llm.PartStart{Index: partIndex, Part: llm.NewTextPart(block.Text)})

	case "thinking":
		partIndex, err := s.addPart(index)
		if err != nil {
			return err
		}
		state := &streamPartState{kind: llm.PartTypeReasoning, signature: block.Signature}
		s.parts[partIndex] = state
		part := llm.NewReasoningPart(block.Thinking)
		if err := attachState(part, partState{Signature: block.Signature}); err != nil {
			return err
		}
		return s.emit(llm.PartStart{Index: partIndex, Part: part})

	case "redacted_thinking":
		if block.Data == "" {
			return fmt.Errorf("%w: redacted thinking block has no data", llm.ErrUnsupportedPart)
		}
		partIndex, err := s.addPart(index)
		if err != nil {
			return err
		}
		s.parts[partIndex] = &streamPartState{kind: llm.PartTypeReasoning, redacted: true}
		part := llm.NewReasoningPart("")
		part.Redacted = true
		if err := attachState(part, partState{RedactedData: block.Data}); err != nil {
			return err
		}
		return s.emit(llm.PartStart{Index: partIndex, Part: part})

	case "tool_use", "server_tool_use":
		return s.startToolCall(index, block)

	default:
		if _, hosted := hostedResultTypes[block.Type]; hosted {
			return s.recordServerResult(block.ToolUseID, block.RawJSON())
		}
		return fmt.Errorf("%w: anthropic content block %q", llm.ErrUnsupportedPartType, block.Type)
	}
}

// startToolCall begins a client or hosted tool call. The start event's input is
// normally an empty object; real arguments arrive as input_json_delta
// fragments, which are emitted as cumulative Replace snapshots.
func (s *streamState) startToolCall(index int64, block anthropic.ContentBlockStartEventContentBlockUnion) error {
	input, err := marshalInput(block.Input)
	if err != nil {
		return err
	}
	callKind := llm.ToolKindFunction
	if block.Type == "server_tool_use" {
		callKind = llm.ToolKindServer
	}
	name := block.Name
	if block.Type == "server_tool_use" {
		name = block.Name
	}

	partIndex, err := s.addPart(index)
	if err != nil {
		return err
	}
	state := &streamPartState{
		kind:     llm.PartTypeToolCall,
		callKind: callKind,
		callID:   block.ID,
		name:     name,
	}
	if string(input) != "{}" {
		state.seeded = true
		state.input.Write(input)
	}
	s.parts[partIndex] = state

	call := llm.ToolCall{
		CallID: block.ID,
		Name:   name,
		Kind:   callKind,
		Input:  input,
		Status: llm.ToolStatusCompleted,
	}
	return s.emit(llm.PartStart{Index: partIndex, Part: llm.NewToolCallPart(call)})
}

func (s *streamState) applyDelta(blockIndex int64, delta anthropic.MessageStreamEventUnionDelta) error {
	partIndex, ok := s.byBlock[blockIndex]
	if !ok {
		return fmt.Errorf("%w: delta for unknown block %d", llm.ErrInvalidPart, blockIndex)
	}
	part := s.parts[partIndex]
	if part == nil {
		return fmt.Errorf("%w: missing part state for block %d", llm.ErrInvalidPart, blockIndex)
	}

	switch delta.Type {
	case "text_delta":
		if part.kind != llm.PartTypeText {
			return fmt.Errorf("%w: text delta on %s part", llm.ErrInvalidPart, part.kind)
		}
		return s.emit(llm.PartDelta{Index: partIndex, Kind: llm.PartTypeText, Text: delta.Text})

	case "thinking_delta":
		if part.kind != llm.PartTypeReasoning || part.redacted {
			return fmt.Errorf("%w: thinking delta on %s part", llm.ErrInvalidPart, part.kind)
		}
		return s.emit(llm.PartDelta{Index: partIndex, Kind: llm.PartTypeReasoning, Text: delta.Thinking})

	case "signature_delta":
		if part.kind != llm.PartTypeReasoning || part.redacted {
			return fmt.Errorf("%w: signature delta on %s part", llm.ErrInvalidPart, part.kind)
		}
		part.signature += delta.Signature
		if part.signature == "" {
			return nil
		}
		return s.emit(llm.PartDelta{
			Index:    partIndex,
			Kind:     llm.PartTypeReasoning,
			Provider: providerDataFor(partState{Signature: part.signature}),
		})

	case "input_json_delta":
		if part.kind != llm.PartTypeToolCall {
			return fmt.Errorf("%w: input delta on %s part", llm.ErrInvalidPart, part.kind)
		}
		if part.seeded {
			return fmt.Errorf("%w: input delta after a complete tool input", llm.ErrInvalidPart)
		}
		part.input.WriteString(delta.PartialJSON)
		return s.emit(llm.PartDelta{
			Index:   partIndex,
			Kind:    llm.PartTypeToolCall,
			Replace: true,
			Call: &llm.ToolCallDelta{
				CallID:        part.callID,
				Name:          part.name,
				InputFragment: part.input.String(),
			},
		})

	case "citations_delta":
		return fmt.Errorf("%w: anthropic citations", llm.ErrUnsupported)

	default:
		return fmt.Errorf("%w: anthropic delta %q", llm.ErrUnsupported, delta.Type)
	}
}

func (s *streamState) stopBlock(blockIndex int64) error {
	partIndex, ok := s.byBlock[blockIndex]
	if !ok {
		// Hosted result blocks are recorded, not streamed as canonical parts.
		return nil
	}
	if s.ended[partIndex] {
		return fmt.Errorf("%w: duplicate content_block_stop for %d", llm.ErrInvalidPart, blockIndex)
	}
	// A hosted call's result block arrives after the call block closes, and the
	// recorded result still needs to be attached to the call part. Keep the
	// part open until the result arrives or the message ends.
	if part := s.parts[partIndex]; part != nil &&
		part.kind == llm.PartTypeToolCall && part.callKind == llm.ToolKindServer && !part.hasResult {
		return nil
	}
	s.ended[partIndex] = true
	return s.emit(llm.PartEnd{Index: partIndex})
}

// addPart assigns the next canonical index to an Anthropic block.
func (s *streamState) addPart(blockIndex int64) (int, error) {
	if _, duplicate := s.byBlock[blockIndex]; duplicate {
		return 0, fmt.Errorf("%w: duplicate content_block_start for %d", llm.ErrInvalidPart, blockIndex)
	}
	index := s.nextIndex
	s.nextIndex++
	s.byBlock[blockIndex] = index
	return index, nil
}

// recordServerResult stores a hosted result block on the server tool call it
// answers. The client never executes hosted tools.
func (s *streamState) recordServerResult(callID, raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: hosted result has no JSON", llm.ErrUnsupportedPart)
	}
	for index := s.nextIndex - 1; index >= 0; index-- {
		part := s.parts[index]
		if part == nil || part.kind != llm.PartTypeToolCall || part.callKind != llm.ToolKindServer {
			continue
		}
		if callID != "" && part.callID != callID {
			continue
		}
		if part.hasResult {
			return fmt.Errorf("%w: duplicate hosted result for %q", llm.ErrUnsupportedPart, callID)
		}
		part.hasResult = true
		part.serverData = json.RawMessage(raw)
		if err := s.emit(llm.PartDelta{
			Index:    index,
			Kind:     llm.PartTypeToolCall,
			Provider: providerDataFor(partState{ServerResult: part.serverData}),
			Call: &llm.ToolCallDelta{
				CallID: part.callID,
				Name:   part.name,
			},
		}); err != nil {
			return err
		}
		// The call block already stopped; close the canonical part now that its
		// recorded result is attached.
		if !s.ended[index] {
			s.ended[index] = true
			return s.emit(llm.PartEnd{Index: index})
		}
		return nil
	}
	return fmt.Errorf("%w: hosted result for unknown server tool call %q", llm.ErrUnsupportedPart, callID)
}

// finish ends every open part and emits the terminal message event.
func (s *streamState) finish() error {
	if !s.messageStarted {
		return fmt.Errorf("%w: anthropic stream produced no message_start", llm.ErrInvalidMessage)
	}

	if s.nextIndex == 0 && s.stopReason == anthropic.StopReasonRefusal {
		if err := s.emit(llm.PartStart{
			Index: 0,
			Part:  llm.NewRefusalPart(refusalText(s.stopDetails.Explanation)),
		}); err != nil {
			return err
		}
		s.parts[0] = &streamPartState{kind: llm.PartTypeRefusal}
		s.nextIndex = 1
	}
	if s.nextIndex == 0 {
		return fmt.Errorf("%w: anthropic stream produced no content", llm.ErrInvalidMessage)
	}

	for index := 0; index < s.nextIndex; index++ {
		part := s.parts[index]
		if part == nil {
			return fmt.Errorf("%w: missing part at index %d", llm.ErrInvalidMessage, index)
		}
		if part.kind == llm.PartTypeReasoning && !part.redacted && part.signature == "" {
			return fmt.Errorf("%w: thinking block %d has no signature", llm.ErrUnsupportedPart, index)
		}
		if !s.ended[index] {
			s.ended[index] = true
			if err := s.emit(llm.PartEnd{Index: index}); err != nil {
				return err
			}
		}
	}

	finish, status := mapStopReason(s.stopReason)
	if finish == "" {
		finish = llm.FinishReasonStop
	}
	return s.emit(llm.MessageEnd{
		Status:          status,
		FinishReason:    finish,
		RawFinishReason: string(s.stopReason),
		Usage:           s.usage,
	})
}

// applyDeltaUsage overlays the cumulative usage snapshot from message_delta.
func applyDeltaUsage(current *llm.Usage, usage anthropic.MessageDeltaUsage) {
	if usage.JSON.InputTokens.Valid() {
		current.PromptTokens = int(usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens)
		current.CachedTokens = int(usage.CacheReadInputTokens)
	}
	if usage.JSON.OutputTokens.Valid() {
		current.CompletionTokens = int(usage.OutputTokens)
	}
	if usage.OutputTokensDetails.JSON.ThinkingTokens.Valid() {
		current.ReasoningTokens = int(usage.OutputTokensDetails.ThinkingTokens)
	}
	current.TotalTokens = current.PromptTokens + current.CompletionTokens
}
