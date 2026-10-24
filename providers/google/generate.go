package google

import (
	"context"
	"encoding/json"
	"errors"
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

	contents, systemInstruction, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	config := m.convertConfig(req, systemInstruction)

	resp, err := m.client.Models.GenerateContent(ctx, m.modelID, contents, config)
	if err != nil {
		return nil, wrapError(err)
	}
	if err := blockedResponseError(resp); err != nil {
		return nil, err
	}

	text, thinking := extractCandidateTextAndThinking(resp.Candidates)
	finish := llm.FinishReasonStop
	if len(resp.Candidates) > 0 && resp.Candidates[0].FinishReason != "" {
		finish = mapFinishReason(resp.Candidates[0].FinishReason)
	}

	toolCalls := extractToolCalls(resp)
	if len(toolCalls) > 0 && (finish == llm.FinishReasonStop || finish == "") {
		finish = llm.FinishReasonTool
	}

	message := llm.Message{
		Role:     llm.RoleAssistant,
		Thinking: thinking,
	}
	if text != "" {
		message.Content = []llm.Part{llm.TextPart(text)}
	}
	message.ToolCalls = toolCalls
	if len(resp.Candidates) > 0 {
		message.ProviderData = providerDataFromContent(resp.Candidates[0].Content)
	}

	return &llm.Response{
		Message:      message,
		FinishReason: finish,
		Usage:        usageOf(resp),
	}, nil
}

// Stream initiates a streaming generation.
func (m *Model) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		req = &llm.Request{}
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
		emittedCalls := make(map[int]bool)
		iterSeq := m.client.Models.GenerateContentStream(streamCtx, m.modelID, contents, config)
		for resp, recvErr := range iterSeq {
			if recvErr != nil {
				sendError(streamCtx, errCh, wrapError(recvErr))
				return
			}
			if err := blockedResponseError(resp); err != nil {
				sendError(streamCtx, errCh, err)
				return
			}

			var deltaText strings.Builder
			var deltaThinking strings.Builder
			var finish llm.FinishReason

			if len(resp.Candidates) > 0 {
				candidate := resp.Candidates[0]
				if candidate.Content != nil {
					for _, part := range candidate.Content.Parts {
						if isPartValid(part) {
							accumulatedParts = append(accumulatedParts, part)
						}
						if part.Thought {
							deltaThinking.WriteString(part.Text)
						} else if part.Text != "" {
							deltaText.WriteString(part.Text)
						}
					}
				}
				if candidate.FinishReason != "" {
					finish = mapFinishReason(candidate.FinishReason)
				}
			}

			var toolCalls []llm.ToolCall
			for index, call := range extractToolCalls(resp) {
				if emittedCalls[index] {
					continue
				}
				emittedCalls[index] = true
				toolCalls = append(toolCalls, call)
			}
			if len(toolCalls) > 0 && (finish == "" || finish == llm.FinishReasonStop) {
				finish = llm.FinishReasonTool
			}

			var usage *llm.Usage
			if resp.UsageMetadata != nil {
				u := usageOf(resp)
				usage = &u
			}

			var providerData *llm.ProviderData
			if len(accumulatedParts) > 0 {
				providerData = providerDataFromContent(&genai.Content{
					Role:  genai.RoleModel,
					Parts: append([]*genai.Part(nil), accumulatedParts...),
				})
			}

			chunk := &llm.StreamChunk{
				Delta:         deltaText.String(),
				ThinkingDelta: deltaThinking.String(),
				ToolCalls:     toolCalls,
				FinishReason:  finish,
				Usage:         usage,
				ProviderData:  providerData,
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

func extractToolCalls(resp *genai.GenerateContentResponse) []llm.ToolCall {
	calls := resp.FunctionCalls()
	if len(calls) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(calls))
	for index, call := range calls {
		arguments, err := json.Marshal(call.Args)
		if err != nil {
			arguments = []byte("{}")
		}
		out = append(out, llm.ToolCall{
			Index:     index,
			ID:        call.ID,
			Name:      call.Name,
			Arguments: string(arguments),
		})
	}
	return out
}

func usageOf(resp *genai.GenerateContentResponse) llm.Usage {
	if resp == nil || resp.UsageMetadata == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
		CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
		ReasoningTokens:  int(resp.UsageMetadata.ThoughtsTokenCount),
		CachedTokens:     int(resp.UsageMetadata.CachedContentTokenCount),
	}
}

func sendError(ctx context.Context, ch chan<- error, err error) {
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
