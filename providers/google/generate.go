package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// Generate sends a non-streaming request to Gemini.
func (m *Model) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config, err := m.convertConfig(req, systemInstruction)
	if err != nil {
		return nil, err
	}
	if err := m.applyHeaders(ctx, config); err != nil {
		return nil, err
	}

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, wrapError(err)
	}
	if err := blockedResponseError(resp); err != nil {
		return nil, err
	}
	return m.canonicalResponse(resp)
}

// Stream initiates a streaming generation.
func (m *Model) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		req = &llm.Request{}
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config, err := m.convertConfig(req, systemInstruction)
	if err != nil {
		return nil, err
	}
	if err := m.applyHeaders(ctx, config); err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan llm.StreamEvent, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errCh)

		state := &streamState{ctx: streamCtx, events: events, model: m.modelID}
		started := false
		for resp, recvErr := range m.client.Models.GenerateContentStream(streamCtx, m.modelID, contents, config) {
			if recvErr != nil {
				sendStreamError(streamCtx, errCh, wrapError(recvErr))
				return
			}
			if resp == nil {
				continue
			}
			if err := blockedResponseError(resp); err != nil {
				sendStreamError(streamCtx, errCh, err)
				return
			}
			if !started {
				started = true
				modelID := resp.ModelVersion
				if modelID == "" {
					modelID = m.modelID
				}
				if err := state.emit(llm.MessageStart{ID: resp.ResponseID, Model: modelID}); err != nil {
					return
				}
			}
			if err := state.applyChunk(resp); err != nil {
				if streamCtx.Err() != nil {
					return
				}
				sendStreamError(streamCtx, errCh, err)
				return
			}
		}

		if streamCtx.Err() != nil {
			return
		}
		if !started {
			sendStreamError(streamCtx, errCh, fmt.Errorf("%w: google stream produced no responses", llm.ErrInvalidMessage))
			return
		}
		if err := state.finish(); err != nil {
			if streamCtx.Err() != nil {
				return
			}
			sendStreamError(streamCtx, errCh, err)
		}
	}()

	return llm.NewChannelStream(streamCtx, cancel, events, errCh), nil
}

// incomingPart is one canonical part recovered from a Gemini stream chunk.
type incomingPart struct {
	kind          llm.PartType
	text          string
	call          llm.ToolCall
	file          *llm.FilePart
	signature     []byte
	signatureOnly bool
}

// streamPart is the accumulator for one canonical part across chunks.
type streamPart struct {
	kind      llm.PartType
	text      string
	call      llm.ToolCall
	rawCallID string
	file      *llm.FilePart
	signature []byte
	started   bool
}

// streamState maps Gemini's per-chunk parts onto canonical part indices.
//
// Gemini may send incremental parts or cumulative snapshots. A chunk that
// repeats earlier parts is overlaid on the same canonical indices; a chunk
// that starts a new part after a different kind is appended. Text snapshots
// are emitted with Replace so the assembler never double-appends them.
type streamState struct {
	ctx    context.Context
	events chan<- llm.StreamEvent
	model  string

	parts []streamPart
	slots []int

	finishReason llm.FinishReason
	rawFinish    string
	usage        llm.Usage
}

func (s *streamState) emit(event llm.StreamEvent) error {
	select {
	case s.events <- event:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *streamState) applyChunk(resp *genai.GenerateContentResponse) error {
	if resp == nil {
		return nil
	}
	if resp.UsageMetadata != nil {
		// Usage is a terminal snapshot, never a sum of chunks (invariant 14).
		s.usage = usageOfMetadata(resp.UsageMetadata)
	}

	candidate := firstCandidate(resp)
	if candidate == nil {
		return nil
	}
	if candidate.FinishReason != "" && candidate.FinishReason != genai.FinishReasonUnspecified {
		s.rawFinish = string(candidate.FinishReason)
		s.finishReason = mapFinishReason(candidate.FinishReason)
	}
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return nil
	}

	incoming := make([]incomingPart, 0, len(candidate.Content.Parts))
	for _, part := range candidate.Content.Parts {
		converted, err := incomingFromGenai(part)
		if err != nil {
			return err
		}
		if converted == nil {
			continue
		}
		incoming = append(incoming, *converted)
	}

	ordinary := make([]incomingPart, 0, len(incoming))
	ordinaryAt := make([]int, 0, len(incoming))
	for index := range incoming {
		if incoming[index].signatureOnly {
			continue
		}
		ordinary = append(ordinary, incoming[index])
		ordinaryAt = append(ordinaryAt, index)
	}
	var slots []int
	if len(ordinary) > 0 {
		slots = s.assignSlots(ordinary)
		for index := range ordinary {
			if err := s.apply(ordinary[index], slots[index]); err != nil {
				return err
			}
		}
		s.slots = slots
	}

	for index := range incoming {
		if !incoming[index].signatureOnly {
			continue
		}
		slot := -1
		for position := len(ordinaryAt) - 1; position >= 0; position-- {
			if ordinaryAt[position] < index {
				slot = slots[position]
				break
			}
		}
		if err := s.applySignature(slot, incoming[index].signature); err != nil {
			return err
		}
	}
	return nil
}

// applySignature attaches a signature-only part to the part it signs. When the
// chunk does not say which part that is, the most recent text-like part is
// used.
func (s *streamState) applySignature(slot int, signature []byte) error {
	if len(signature) == 0 {
		return nil
	}
	if slot < 0 || slot >= len(s.parts) || !s.parts[slot].started {
		slot = s.lastTextSlot()
	}
	if slot < 0 {
		// No preceding content: keep the signature as an empty reasoning part
		// so it survives replay.
		slot = len(s.parts)
		s.parts = append(s.parts, streamPart{
			kind:      llm.PartTypeReasoning,
			signature: append([]byte(nil), signature...),
			started:   true,
		})
		canonical := llm.NewReasoningPart("")
		if err := attachProviderState(canonical, partProviderState{ThoughtSignature: signature}); err != nil {
			return err
		}
		return s.emit(llm.PartStart{Index: slot, Part: canonical})
	}

	part := &s.parts[slot]
	if part.kind == llm.PartTypeFile {
		// Files carry no signed payload of their own.
		return nil
	}
	part.signature = append(part.signature[:0], signature...)
	event := llm.PartDelta{
		Index:    slot,
		Kind:     part.kind,
		Provider: providerDataFor(partProviderState{ThoughtSignature: signature}),
	}
	if part.kind == llm.PartTypeToolCall {
		event.Call = &llm.ToolCallDelta{
			ItemID: part.call.ItemID,
			CallID: part.call.CallID,
			Name:   part.call.Name,
		}
	}
	return s.emit(event)
}

func (s *streamState) lastTextSlot() int {
	for index := len(s.parts) - 1; index >= 0; index-- {
		switch s.parts[index].kind {
		case llm.PartTypeText, llm.PartTypeReasoning:
			if s.parts[index].started {
				return index
			}
		}
	}
	return -1
}

func incomingFromGenai(part *genai.Part) (*incomingPart, error) {
	if part == nil {
		return nil, nil
	}
	if err := rejectServerPart(part); err != nil {
		return nil, err
	}

	payloads := 0
	if part.FunctionCall != nil {
		payloads++
	}
	if part.InlineData != nil {
		payloads++
	}
	if part.FileData != nil {
		payloads++
	}
	if part.Text != "" {
		payloads++
	}
	if payloads > 1 {
		return nil, fmt.Errorf("%w: Gemini part has multiple payloads", llm.ErrUnsupportedPart)
	}

	switch {
	case part.FunctionCall != nil:
		call := part.FunctionCall
		if len(call.PartialArgs) > 0 {
			return nil, fmt.Errorf("%w: partial function-call arguments", llm.ErrUnsupported)
		}
		input, err := json.Marshal(call.Args)
		if err != nil {
			return nil, fmt.Errorf("google: encode function call args for %q: %w", call.Name, err)
		}
		if len(call.Args) == 0 {
			input = json.RawMessage("{}")
		}
		return &incomingPart{
			kind: llm.PartTypeToolCall,
			call: llm.ToolCall{
				CallID: call.ID,
				Name:   call.Name,
				Kind:   llm.ToolKindFunction,
				Input:  input,
				Status: llm.ToolStatusCompleted,
			},
			signature: part.ThoughtSignature,
		}, nil
	case part.InlineData != nil:
		if part.InlineData.MIMEType == "" || len(part.InlineData.Data) == 0 {
			return nil, fmt.Errorf("%w: inline data is missing a MIME type or bytes", llm.ErrInvalidPart)
		}
		file := llm.FileFromBytes(part.InlineData.Data, part.InlineData.MIMEType)
		file.Filename = part.InlineData.DisplayName
		return &incomingPart{kind: llm.PartTypeFile, file: file}, nil
	case part.FileData != nil:
		if part.FileData.MIMEType == "" || part.FileData.FileURI == "" {
			return nil, fmt.Errorf("%w: file data is missing a MIME type or URI", llm.ErrInvalidPart)
		}
		file := llm.FileFromURL(part.FileData.FileURI, part.FileData.MIMEType)
		file.Filename = part.FileData.DisplayName
		return &incomingPart{kind: llm.PartTypeFile, file: file}, nil
	case part.Thought:
		if part.Text == "" && len(part.ThoughtSignature) == 0 {
			return nil, nil
		}
		return &incomingPart{kind: llm.PartTypeReasoning, text: part.Text, signature: part.ThoughtSignature}, nil
	case part.Text != "":
		return &incomingPart{kind: llm.PartTypeText, text: part.Text, signature: part.ThoughtSignature}, nil
	case len(part.ThoughtSignature) > 0:
		// A signature without a payload belongs to the preceding content
		// part. applyChunk attaches it there instead of inventing a part.
		return &incomingPart{signature: part.ThoughtSignature, signatureOnly: true}, nil
	default:
		return nil, nil
	}
}

// assignSlots chooses the canonical index for every incoming part.
//
// Gemini streams either cumulative snapshots that repeat the whole part array
// or incremental chunks that carry only new parts. A chunk that matches the
// beginning of the existing parts is a snapshot; a chunk that matches the end
// continues the trailing parts. Anything else starts a new part. Position is
// the only identity Gemini provides, so part order is authoritative.
func (s *streamState) assignSlots(incoming []incomingPart) []int {
	slots := make([]int, len(incoming))
	for i := range slots {
		slots[i] = -1
	}

	if n := len(s.slots); n > 0 {
		switch {
		case len(incoming) >= n && s.matchesSlots(s.slots, incoming[:n]):
			copy(slots[:n], s.slots)
		case len(incoming) < n && s.matchesSlots(s.slots[n-len(incoming):], incoming):
			copy(slots, s.slots[n-len(incoming):])
		}
	}

	for index := range incoming {
		if slots[index] >= 0 {
			continue
		}
		if incoming[index].kind == llm.PartTypeToolCall {
			if slot := s.findCallSlot(incoming[index].call); slot >= 0 {
				slots[index] = slot
				continue
			}
		}
		slots[index] = len(s.parts)
		s.parts = append(s.parts, streamPart{kind: incoming[index].kind})
	}
	return slots
}

// matchesSlots reports whether incoming parts line up with the given slots.
func (s *streamState) matchesSlots(slots []int, incoming []incomingPart) bool {
	if len(slots) != len(incoming) {
		return false
	}
	for index, slot := range slots {
		if slot < 0 || slot >= len(s.parts) {
			return false
		}
		existing := &s.parts[slot]
		candidate := incoming[index]
		if existing.kind != candidate.kind {
			return false
		}
		switch candidate.kind {
		case llm.PartTypeToolCall:
			if !sameCall(existing, candidate.call) {
				return false
			}
		case llm.PartTypeFile:
			if !sameFile(existing.file, candidate.file) {
				return false
			}
		}
	}
	return true
}

func (s *streamState) findCallSlot(call llm.ToolCall) int {
	for index := range s.parts {
		if s.parts[index].kind != llm.PartTypeToolCall {
			continue
		}
		if sameCall(&s.parts[index], call) {
			return index
		}
	}
	return -1
}

func sameCall(existing *streamPart, call llm.ToolCall) bool {
	if existing == nil {
		return false
	}
	if existing.rawCallID != "" && call.CallID != "" {
		return existing.rawCallID == call.CallID
	}
	return existing.call.Name == call.Name && bytes.Equal(existing.call.Input, call.Input)
}

func sameFile(existing, candidate *llm.FilePart) bool {
	if existing == nil || candidate == nil {
		return existing == candidate
	}
	return existing.MIMEType == candidate.MIMEType &&
		bytes.Equal(existing.Data, candidate.Data) &&
		existing.URL == candidate.URL &&
		existing.FileID == candidate.FileID
}

func (s *streamState) apply(incoming incomingPart, slot int) error {
	if slot < 0 || slot >= len(s.parts) {
		return fmt.Errorf("%w: canonical slot %d", llm.ErrInvalidPart, slot)
	}
	part := &s.parts[slot]
	switch incoming.kind {
	case llm.PartTypeText, llm.PartTypeReasoning:
		return s.applyText(part, slot, incoming)
	case llm.PartTypeToolCall:
		return s.applyToolCall(part, slot, incoming)
	case llm.PartTypeFile:
		if part.started {
			return nil
		}
		part.started = true
		part.file = incoming.file
		return s.emit(llm.PartStart{Index: slot, Part: incoming.file})
	default:
		return fmt.Errorf("%w: streamed part type %q", llm.ErrUnsupportedPartType, incoming.kind)
	}
}

func (s *streamState) applyText(part *streamPart, slot int, incoming incomingPart) error {
	if !part.started {
		part.started = true
		part.text = incoming.text
		part.signature = append([]byte(nil), incoming.signature...)
		canonical := newStreamTextPart(incoming.kind, incoming.text)
		if err := attachProviderState(canonical, partProviderState{ThoughtSignature: incoming.signature}); err != nil {
			return err
		}
		return s.emit(llm.PartStart{Index: slot, Part: canonical})
	}

	event := llm.PartDelta{
		Index:    slot,
		Kind:     incoming.kind,
		Provider: providerDataFor(partProviderState{ThoughtSignature: incoming.signature}),
	}
	switch {
	case incoming.text == part.text:
	case strings.HasPrefix(incoming.text, part.text):
		event.Replace = true
		event.Text = incoming.text
		part.text = incoming.text
	case strings.HasPrefix(part.text, incoming.text):
		// An older snapshot arrived out of order; keep the longer text.
	default:
		event.Text = incoming.text
		part.text += incoming.text
	}
	if len(incoming.signature) > 0 {
		part.signature = append(part.signature[:0], incoming.signature...)
	}

	if event.Text == "" && len(event.Provider) == 0 {
		return nil
	}
	return s.emit(event)
}

func (s *streamState) applyToolCall(part *streamPart, slot int, incoming incomingPart) error {
	call := incoming.call
	if !part.started {
		part.started = true
		part.rawCallID = call.CallID
		if call.CallID == "" {
			// Gemini omits function-call IDs on some models. Persist a stable
			// ID so results can reference it after a store round-trip.
			call.CallID = fmt.Sprintf("call_%d", slot)
		}
		if len(call.Input) == 0 {
			call.Input = json.RawMessage("{}")
		}
		part.call = call
		part.signature = append([]byte(nil), incoming.signature...)
		canonical := llm.NewToolCallPart(call)
		if err := attachProviderState(canonical, partProviderState{ThoughtSignature: incoming.signature}); err != nil {
			return err
		}
		return s.emit(llm.PartStart{Index: slot, Part: canonical})
	}

	changed := call.Name != part.call.Name || !bytes.Equal(call.Input, part.call.Input)
	if !changed && bytes.Equal(incoming.signature, part.signature) {
		return nil
	}
	part.call.Name = call.Name
	part.call.Input = append(part.call.Input[:0], call.Input...)
	if call.ItemID != "" {
		part.call.ItemID = call.ItemID
	}
	if len(incoming.signature) > 0 {
		part.signature = append(part.signature[:0], incoming.signature...)
	}

	return s.emit(llm.PartDelta{
		Index:    slot,
		Kind:     llm.PartTypeToolCall,
		Replace:  true,
		Provider: providerDataFor(partProviderState{ThoughtSignature: incoming.signature}),
		Call: &llm.ToolCallDelta{
			ItemID:        part.call.ItemID,
			CallID:        part.call.CallID,
			Name:          part.call.Name,
			InputSnapshot: part.call.Input,
		},
	})
}

func (s *streamState) finish() error {
	if len(s.parts) == 0 {
		return fmt.Errorf("%w: google stream produced no content", llm.ErrInvalidMessage)
	}
	for index := range s.parts {
		if err := s.emit(llm.PartEnd{Index: index}); err != nil {
			return err
		}
	}

	finish := s.finishReason
	if finish == "" {
		finish = llm.FinishReasonStop
	}
	if finish == llm.FinishReasonStop && s.hasToolCalls() {
		finish = llm.FinishReasonTool
	}
	return s.emit(llm.MessageEnd{
		Status:          messageStatus(finish),
		FinishReason:    finish,
		RawFinishReason: s.rawFinish,
		Usage:           s.usage,
	})
}

func (s *streamState) hasToolCalls() bool {
	for index := range s.parts {
		if s.parts[index].kind == llm.PartTypeToolCall {
			return true
		}
	}
	return false
}

func newStreamTextPart(kind llm.PartType, text string) llm.Part {
	if kind == llm.PartTypeReasoning {
		return llm.NewReasoningPart(text)
	}
	return llm.NewTextPart(text)
}

func sendStreamError(ctx context.Context, ch chan<- error, err error) {
	select {
	case ch <- err:
	case <-ctx.Done():
	}
}

// wrapError maps transport failures to the provider-neutral error taxonomy.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	if code, ok := apiErrorCode(err); ok {
		return &llm.Error{Kind: classifyStatus(code), StatusCode: code, Err: err}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &llm.Error{Kind: llm.KindTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return &llm.Error{Kind: llm.KindCanceled, Err: err}
	}
	return err
}

// apiErrorCode extracts the HTTP status from either form the SDK returns:
// some paths box genai.APIError by value and others by pointer.
func apiErrorCode(err error) (int, bool) {
	var pointer *genai.APIError
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Code, true
	}
	var value genai.APIError
	if errors.As(err, &value) {
		return value.Code, true
	}
	return 0, false
}

func classifyStatus(code int) llm.Kind {
	switch code {
	case 400, 404, 422:
		return llm.KindInvalid
	case 401, 403:
		return llm.KindAuth
	case 408:
		return llm.KindTimeout
	case 429:
		return llm.KindRateLimit
	case 500, 502, 503, 504:
		return llm.KindOverloaded
	default:
		return llm.KindProvider
	}
}
