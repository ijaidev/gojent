package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

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
	streamer, ok := r.opts.Model.(llm.Streamer)
	if !ok {
		response, err := callGenerate(ctx, r.opts.Model, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, ErrResponseRequired
		}
		message := r.finalizeAssistant(response.Message, step)
		emitMessageDeltasFromMessage(r.opts.Sink, step, message)
		safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
		return &llm.Response{Message: message, FinishReason: response.FinishReason, Usage: response.Usage}, nil
	}

	stream, err := callStream(ctx, streamer, request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.Close()
	}()

	safeEmit(r.opts.Sink, MessageStart{Step: step})
	var text, thinking string
	var calls []llm.ToolCall
	var usage llm.Usage
	var finish llm.FinishReason
	var providerData *llm.ProviderData

	for {
		chunk, recvErr := recvStream(stream)
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if chunk.Delta != "" {
			text += chunk.Delta
			safeEmit(r.opts.Sink, TextDelta{Step: step, Text: chunk.Delta})
		}
		if chunk.ThinkingDelta != "" {
			thinking += chunk.ThinkingDelta
			safeEmit(r.opts.Sink, ThinkingDelta{Step: step, Text: chunk.ThinkingDelta})
		}
		if len(chunk.ToolCalls) > 0 {
			calls = accumulateToolCalls(calls, chunk.ToolCalls)
			for _, call := range chunk.ToolCalls {
				safeEmit(r.opts.Sink, ToolCallDelta{
					Step:      step,
					Index:     call.Index,
					ID:        call.ID,
					Name:      call.Name,
					Arguments: call.Arguments,
				})
			}
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = usage.Add(*chunk.Usage)
		}
		if chunk.ProviderData != nil {
			providerData = chunk.ProviderData.Clone()
		}
	}

	message := llm.Message{
		Role:         llm.RoleAssistant,
		Thinking:     thinking,
		ToolCalls:    calls,
		ProviderData: providerData,
	}
	if text != "" {
		message.Content = []llm.Part{llm.TextPart(text)}
	}
	message = r.finalizeAssistant(message, step)
	if finish == "" {
		if len(message.ToolCalls) > 0 {
			finish = llm.FinishReasonTool
		} else {
			finish = llm.FinishReasonStop
		}
	}
	safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
	return &llm.Response{Message: message, FinishReason: finish, Usage: usage}, nil
}

func (r *runner) finalizeAssistant(message llm.Message, step int) llm.Message {
	message.Role = llm.RoleAssistant
	message.Content = llm.NonEmptyParts(message.Content)
	for i := range message.ToolCalls {
		call := &message.ToolCalls[i]
		call.Index = i
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		if call.ID == "" || r.usedCallID[call.ID] {
			// A generated ID must not collide with one the provider already
			// used, including a provider ID that happens to look generated.
			base := fmt.Sprintf("call_%d_%d", step, i)
			candidate := base
			for suffix := 1; r.usedCallID[candidate]; suffix++ {
				candidate = fmt.Sprintf("%s_%d", base, suffix)
			}
			call.ID = candidate
		}
		r.usedCallID[call.ID] = true
	}
	return message
}

// accumulateToolCalls merges streamed fragments by ID or index.
func accumulateToolCalls(existing, incoming []llm.ToolCall) []llm.ToolCall {
	for _, fragment := range incoming {
		index := -1
		if fragment.ID != "" {
			for i := range existing {
				if existing[i].ID == fragment.ID {
					index = i
					break
				}
			}
		}
		if index < 0 && fragment.ID == "" {
			for i := range existing {
				if existing[i].Index == fragment.Index {
					index = i
					break
				}
			}
		}
		if index < 0 && fragment.ID != "" {
			for i := range existing {
				if existing[i].Index == fragment.Index && existing[i].ID == "" {
					index = i
					break
				}
			}
		}
		if index < 0 {
			existing = append(existing, fragment)
			continue
		}
		target := &existing[index]
		if target.ID == "" {
			target.ID = fragment.ID
		}
		if target.Name == "" {
			target.Name = fragment.Name
		}
		if fragment.Index != 0 || target.Index == 0 {
			target.Index = fragment.Index
		}
		target.Arguments += fragment.Arguments
	}
	return existing
}

func emitMessageDeltasFromMessage(sink Sink, step int, message llm.Message) {
	safeEmit(sink, MessageStart{Step: step})
	if text := message.Text(); text != "" {
		safeEmit(sink, TextDelta{Step: step, Text: text})
	}
	if message.Thinking != "" {
		safeEmit(sink, ThinkingDelta{Step: step, Text: message.Thinking})
	}
	for _, call := range message.ToolCalls {
		safeEmit(sink, ToolCallDelta{
			Step:      step,
			Index:     call.Index,
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		})
	}
}
