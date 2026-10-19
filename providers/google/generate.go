package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// Generate sends a non-streaming completion request to the Gemini model.
func (m *Model) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config := m.convertConfig(req, systemInstruction)

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, err
	}

	if err := blockedResponseError(resp); err != nil {
		return nil, err
	}

	text, thinking := extractCandidateTextAndThinking(resp.Candidates)
	finishReason := llm.FinishReasonStop
	if len(resp.Candidates) > 0 && resp.Candidates[0].FinishReason != "" {
		finishReason = mapFinishReason(resp.Candidates[0].FinishReason)
	}

	var toolCalls []llm.ToolCall
	for _, fc := range resp.FunctionCalls() {
		argsBytes, _ := json.Marshal(fc.Args)
		callID := fc.ID
		if callID == "" {
			callID = fmt.Sprintf("call_%s", fc.Name)
		}
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        callID,
			Name:      fc.Name,
			Arguments: string(argsBytes),
		})
	}
	if len(toolCalls) > 0 && finishReason == llm.FinishReasonStop {
		finishReason = llm.FinishReasonTool
	}

	var usage llm.Usage
	if resp.UsageMetadata != nil {
		usage = llm.Usage{
			PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
			ReasoningTokens:  int(resp.UsageMetadata.ThoughtsTokenCount),
		}
	}

	return &llm.GenerateResponse{
		Text:         text,
		Thinking:     thinking,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
		Raw:          resp,
	}, nil
}

// Stream initiates a streaming generation from the Gemini model, returning token deltas via llm.Stream.
func (m *Model) Stream(ctx context.Context, req llm.GenerateRequest) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config := m.convertConfig(req, systemInstruction)

	streamCtx, cancel := context.WithCancel(ctx)
	ch := make(chan *llm.StreamChunk)
	errCh := make(chan error, 1)

	go func() {
		defer close(ch)
		defer close(errCh)

		var accumulatedParts []*genai.Part
		iterSeq := m.client.Models.GenerateContentStream(streamCtx, m.modelID, contents, config)
		for resp, err := range iterSeq {
			if err != nil {
				select {
				case errCh <- err:
				case <-streamCtx.Done():
				}
				return
			}

			if err := blockedResponseError(resp); err != nil {
				select {
				case errCh <- err:
				case <-streamCtx.Done():
				}
				return
			}

			var deltaText strings.Builder
			var deltaThinking strings.Builder
			var finishReason llm.FinishReason
			var usage *llm.Usage

			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.Content != nil {
					for _, p := range cand.Content.Parts {
						if isPartValid(p) {
							accumulatedParts = append(accumulatedParts, p)
						}
						if p.Thought {
							deltaThinking.WriteString(p.Text)
						} else if p.Text != "" {
							deltaText.WriteString(p.Text)
						}
					}
				}
				if cand.FinishReason != "" {
					finishReason = mapFinishReason(cand.FinishReason)
				}
			}

			var toolCalls []llm.ToolCall
			for _, fc := range resp.FunctionCalls() {
				argsBytes, _ := json.Marshal(fc.Args)
				callID := fc.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%s", fc.Name)
				}
				toolCalls = append(toolCalls, llm.ToolCall{
					ID:        callID,
					Name:      fc.Name,
					Arguments: string(argsBytes),
				})
			}
			if len(toolCalls) > 0 && (finishReason == "" || finishReason == llm.FinishReasonStop) {
				finishReason = llm.FinishReasonTool
			}

			if resp.UsageMetadata != nil {
				usage = &llm.Usage{
					PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
					CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
					TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
					ReasoningTokens:  int(resp.UsageMetadata.ThoughtsTokenCount),
				}
			}

			var rawContent *genai.Content
			if len(accumulatedParts) > 0 {
				// Share the accumulated parts with the emitted chunk instead of
				// copying them on every chunk: parts are only ever appended, so
				// previously emitted chunks keep their snapshot (old backing
				// array) and never observe later parts.
				rawContent = &genai.Content{
					Role:  genai.RoleModel,
					Parts: accumulatedParts,
				}
			}

			chunk := &llm.StreamChunk{
				Delta:         deltaText.String(),
				ThinkingDelta: deltaThinking.String(),
				ToolCalls:     toolCalls,
				FinishReason:  finishReason,
				Usage:         usage,
				Raw:           rawContent,
			}

			select {
			case ch <- chunk:
			case <-streamCtx.Done():
				return
			}
		}
	}()

	return llm.NewChannelStream(streamCtx, cancel, ch, errCh), nil
}
