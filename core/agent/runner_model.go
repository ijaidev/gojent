package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

// modelStep runs one policy-wrapped model call. The caller owns StepStart and
// StepEnd because a successful step continues into tool execution and
// transcript persistence.
func (r *runner) modelStep(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	if r.opts.Policy != nil {
		modelRequest := &ModelRequest{Step: step, Request: request}
		if err := callPolicy(func() error {
			return r.opts.Policy.BeforeModel(ctx, modelRequest)
		}); err != nil {
			return nil, err
		}
		if modelRequest.Request != nil {
			request = modelRequest.Request
		}
	}

	response, callErr := r.callModel(ctx, request, step)
	if r.opts.Policy != nil {
		modelResult := &ModelResult{
			Step:     step,
			Request:  request,
			Response: response,
			Err:      callErr,
		}
		if err := callPolicy(func() error {
			return r.opts.Policy.AfterModel(ctx, modelResult)
		}); err != nil {
			return nil, err
		}
		response, callErr = modelResult.Response, modelResult.Err
	}
	if callErr == nil && response == nil {
		callErr = ErrResponseRequired
	}
	if callErr != nil {
		if ctx.Err() != nil {
			return nil, r.contextError(ctx)
		}
		return nil, callErr
	}
	return response, nil
}

func (r *runner) callModel(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	attempts := r.opts.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := r.callModelOnce(ctx, request, step)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt == attempts || !retryable(err) {
			return nil, err
		}
		delay := r.opts.Retry.delay(attempt)
		if providerErr, ok := llm.AsError(err); ok && providerErr.RetryAfter > 0 {
			delay = providerErr.RetryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, r.contextError(ctx)
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (r *runner) callModelOnce(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	if err := checkRequestFeatures(r.opts.Model.ModelID(), r.opts.Model.Info().Features, request); err != nil {
		return nil, err
	}

	streamer, ok := r.opts.Model.(llm.Streamer)
	if !ok {
		response, err := callGenerate(ctx, r.opts.Model, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, ErrResponseRequired
		}
		message, err := r.finalizeAssistant(response.Message, step)
		if err != nil {
			return nil, err
		}
		response.Message = message
		emitMessageDeltasFromMessage(r.opts.Sink, step, message)
		safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
		return response, nil
	}

	stream, err := callStream(ctx, streamer, request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.Close()
	}()

	safeEmit(r.opts.Sink, MessageStart{Step: step})
	assembler := llm.NewAssembler(llm.RoleAssistant)
	for {
		event, recvErr := recvStream(stream)
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if event == nil {
			continue
		}
		if err := assembler.Apply(event); err != nil {
			return nil, err
		}
		emitStreamEvent(r.opts.Sink, step, event)
	}

	response, err := assembler.Finish()
	if err != nil {
		return nil, err
	}
	message, err := r.finalizeAssistant(response.Message, step)
	if err != nil {
		return nil, err
	}
	response.Message = message
	if response.FinishReason == "" {
		if len(message.ToolCalls()) > 0 {
			response.FinishReason = llm.FinishReasonTool
		} else {
			response.FinishReason = llm.FinishReasonStop
		}
	}
	safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
	return response, nil
}

// finalizeAssistant assigns stable call IDs and canonical input defaults.
func (r *runner) finalizeAssistant(message llm.Message, step int) (llm.Message, error) {
	message.Role = llm.RoleAssistant
	for i, part := range message.Content {
		call, ok := part.(*llm.ToolCallPart)
		if !ok {
			continue
		}
		if len(call.Input) == 0 {
			call.Input = json.RawMessage("{}")
		}
		if call.CallID == "" || r.usedCallID[call.CallID] {
			base := fmt.Sprintf("call_%d_%d", step, i)
			candidate := base
			for suffix := 1; r.usedCallID[candidate]; suffix++ {
				candidate = fmt.Sprintf("%s_%d", base, suffix)
			}
			call.CallID = candidate
		}
		r.usedCallID[call.CallID] = true
	}
	if err := message.Validate(); err != nil {
		return llm.Message{}, fmt.Errorf("agent: invalid assistant message: %w", err)
	}
	return message, nil
}

func emitStreamEvent(sink Sink, step int, event llm.StreamEvent) {
	switch e := event.(type) {
	case llm.PartStart:
		emitPartSnapshot(sink, step, e.Index, e.Part)
	case llm.PartDelta:
		switch e.Kind {
		case llm.PartTypeText:
			if e.Text != "" {
				safeEmit(sink, TextDelta{Step: step, Index: e.Index, Text: e.Text})
			}
		case llm.PartTypeReasoning:
			if e.Text != "" {
				safeEmit(sink, ThinkingDelta{Step: step, Index: e.Index, Text: e.Text})
			}
		case llm.PartTypeToolCall:
			if e.Call != nil {
				arguments := e.Call.InputFragment
				if e.Call.InputSnapshot != nil {
					arguments = string(e.Call.InputSnapshot)
				}
				safeEmit(sink, ToolCallDelta{
					Step:      step,
					Index:     e.Index,
					ID:        e.Call.CallID,
					Name:      e.Call.Name,
					Arguments: arguments,
				})
			}
		}
	case llm.PartEnd, llm.MessageEnd, llm.ProviderEvent:
		// Terminal metadata is emitted from the assembled response.
	}
}

func emitPartSnapshot(sink Sink, step, index int, part llm.Part) {
	switch p := part.(type) {
	case *llm.TextPart:
		if p.Text != "" {
			safeEmit(sink, TextDelta{Step: step, Index: index, Text: p.Text})
		}
	case *llm.ReasoningPart:
		if p.Text != "" {
			safeEmit(sink, ThinkingDelta{Step: step, Index: index, Text: p.Text})
		}
	case *llm.ToolCallPart:
		safeEmit(sink, ToolCallDelta{
			Step:      step,
			Index:     index,
			ID:        p.CallID,
			Name:      p.Name,
			Arguments: string(p.Input),
		})
	}
}

func emitMessageDeltasFromMessage(sink Sink, step int, message llm.Message) {
	safeEmit(sink, MessageStart{Step: step})
	for i, part := range message.Content {
		emitPartSnapshot(sink, step, i, part)
	}
}
