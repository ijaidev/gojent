package agent

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	// ErrStreamNotFinished is returned when accessing the final agent response before the stream completes.
	ErrStreamNotFinished = errors.New("agent: stream has not finished yet")
	// ErrStreamClosed is returned when operations are attempted on a closed stream.
	ErrStreamClosed = errors.New("agent: stream is closed")
)

// Stream runs the agent with a string prompt and returns an active StreamResponse.
func (a *Agent) Stream(ctx context.Context, prompt string, opts ...GenerateOption) (*StreamResponse, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.StreamMessages(ctx, userMsg, opts...)
}

// StreamParts initiates streaming with multimodal content parts directly.
func (a *Agent) StreamParts(ctx context.Context, parts []llm.Part, opts ...GenerateOption) (*StreamResponse, error) {
	var userMsg []llm.Message
	if len(parts) > 0 {
		userMsg = []llm.Message{llm.UserMessageWithParts(parts...)}
	}
	return a.StreamMessages(ctx, userMsg, opts...)
}

// StreamMessages executes the agent with a slice of chat messages and returns an active StreamResponse.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...GenerateOption) (*StreamResponse, error) {
	r, err := a.newRun(ctx, messages, opts)
	if err != nil {
		return nil, err
	}

	req := r.request(r.messages)
	if err := r.hooks.beforeModel(ctx, 0, &req); err != nil {
		return nil, err
	}

	rawStream, err := r.model.Stream(ctx, req)
	if err != nil {
		r.hooks.afterModel(ctx, 0, &req, nil, err)
		return nil, err
	}

	return newStreamResponse(ctx, r, req, rawStream), nil
}

// StreamResponse manages the streaming lifecycle of an agent response,
// executing tools across ReAct iterations and accumulating tokens, deltas,
// and messages in real time.
type StreamResponse struct {
	mu              sync.Mutex
	ctx             context.Context
	run             *run
	stream          llm.Stream
	messages        []llm.Message
	req             llm.GenerateRequest
	step            int
	stepAccumulated strings.Builder
	stepThinking    strings.Builder
	stepToolCalls   []llm.ToolCall
	stepUsage       llm.Usage
	stepRaw         any
	allToolCalls    []llm.ToolCall
	finishReason    llm.FinishReason
	usage           llm.Usage
	closed          bool
	completed       bool
	err             error
}

// newStreamResponse initializes a new StreamResponse managing multi-step ReAct streaming.
func newStreamResponse(ctx context.Context, r *run, req llm.GenerateRequest, stream llm.Stream) *StreamResponse {
	return &StreamResponse{
		ctx:          ctx,
		run:          r,
		stream:       stream,
		messages:     r.messages,
		req:          req,
		finishReason: llm.FinishReasonStop,
	}
}

// Recv returns the next incremental StreamChunk from the agent's LLM stream.
// When a step completes with tool calls, it automatically executes the tools
// and streams the subsequent turn until the model completes its final response.
// It returns io.EOF when all ReAct iterations have finished.
func (s *StreamResponse) Recv() (*llm.StreamChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		if s.closed {
			return nil, ErrStreamClosed
		}
		if s.completed {
			return nil, io.EOF
		}
		if s.err != nil {
			return nil, s.err
		}

		chunk, err := s.stream.Recv()
		if errors.Is(err, io.EOF) {
			_ = s.stream.Close()

			s.usage.PromptTokens += s.stepUsage.PromptTokens
			s.usage.CompletionTokens += s.stepUsage.CompletionTokens
			s.usage.TotalTokens += s.stepUsage.TotalTokens
			s.usage.ReasoningTokens += s.stepUsage.ReasoningTokens

			completedStep := s.step
			s.run.hooks.afterModel(s.ctx, completedStep, &s.req, &llm.GenerateResponse{
				Text:         s.stepAccumulated.String(),
				Thinking:     s.stepThinking.String(),
				ToolCalls:    s.stepToolCalls,
				FinishReason: s.finishReason,
				Usage:        s.stepUsage,
				Raw:          s.stepRaw,
			}, nil)

			// If the model did not request any tools, it has reached its final answer!
			if len(s.stepToolCalls) == 0 {
				finalMsg := llm.Message{
					Role:     llm.RoleAssistant,
					Content:  []llm.Part{llm.TextPart(s.stepAccumulated.String())},
					Thinking: s.stepThinking.String(),
					Raw:      s.stepRaw,
				}
				s.messages = append(s.messages, finalMsg)
				s.completed = true
				s.run.saveMessages(s.ctx, finalMsg)
				return nil, io.EOF
			}

			// Model requested tool calls in this step
			assistantMsg := llm.Message{
				Role:      llm.RoleAssistant,
				Content:   []llm.Part{llm.TextPart(s.stepAccumulated.String())},
				Thinking:  s.stepThinking.String(),
				ToolCalls: s.stepToolCalls,
				Raw:       s.stepRaw,
			}
			s.messages = append(s.messages, assistantMsg)
			s.allToolCalls = append(s.allToolCalls, s.stepToolCalls...)
			s.run.saveMessages(s.ctx, assistantMsg)

			nextStep := completedStep + 1

			// Execute all requested tool calls
			for _, call := range s.stepToolCalls {
				_, toolParts, _ := s.run.agent.executeToolCall(s.ctx, completedStep, call, s.run.tools, s.run.hooks)
				toolMsg := llm.Message{
					Role:       llm.RoleTool,
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    toolParts,
				}
				s.messages = append(s.messages, toolMsg)
				s.run.saveMessages(s.ctx, toolMsg)
			}

			s.step = nextStep

			// The ReAct budget is exhausted: stop here. The tool calls above
			// were still executed so the transcript never ends with an
			// unanswered tool_calls message, which strict providers reject on
			// the next turn.
			if nextStep >= s.run.maxSteps {
				s.finishReason = llm.FinishReasonLength
				s.completed = true
				return nil, io.EOF
			}

			// Reset step buffers
			s.stepToolCalls = nil
			s.stepAccumulated.Reset()
			s.stepThinking.Reset()
			s.stepUsage = llm.Usage{}
			s.stepRaw = nil

			// Prepare request for the next ReAct iteration
			req := s.run.request(s.messages)
			if err := s.run.hooks.beforeModel(s.ctx, s.step, &req); err != nil {
				s.err = err
				return nil, err
			}

			nextStream, streamErr := s.run.model.Stream(s.ctx, req)
			if streamErr != nil {
				s.run.hooks.afterModel(s.ctx, s.step, &req, nil, streamErr)
				s.err = streamErr
				return nil, streamErr
			}
			s.req = req
			s.stream = nextStream
			continue
		}

		if err != nil {
			s.run.hooks.afterModel(s.ctx, s.step, &s.req, &llm.GenerateResponse{
				Text:      s.stepAccumulated.String(),
				Thinking:  s.stepThinking.String(),
				ToolCalls: s.stepToolCalls,
				Usage:     s.stepUsage,
				Raw:       s.stepRaw,
			}, err)
			s.err = err
			return nil, err
		}

		if chunk.Delta != "" {
			s.stepAccumulated.WriteString(chunk.Delta)
		}
		if chunk.ThinkingDelta != "" {
			s.stepThinking.WriteString(chunk.ThinkingDelta)
		}
		if len(chunk.ToolCalls) > 0 {
			s.stepToolCalls = accumulateToolCalls(s.stepToolCalls, chunk.ToolCalls)
		}
		if chunk.FinishReason != "" {
			s.finishReason = chunk.FinishReason
		}
		if chunk.Usage != nil {
			s.stepUsage = *chunk.Usage
		}
		if chunk.Raw != nil {
			s.stepRaw = chunk.Raw
		}

		return chunk, nil
	}
}

// Close closes the underlying stream and cancels any ongoing generation.
func (s *StreamResponse) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	if s.stream != nil {
		return s.stream.Close()
	}
	return nil
}

// Text returns the text accumulated from stream deltas for the final generation turn.
func (s *StreamResponse) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepAccumulated.String()
}

// Thinking returns the thinking/reasoning text accumulated from stream deltas for the final generation turn.
func (s *StreamResponse) Thinking() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepThinking.String()
}

// ToolCalls returns a copy of all tool calls received across all ReAct steps.
func (s *StreamResponse) ToolCalls() []llm.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := append([]llm.ToolCall{}, s.allToolCalls...)
	if len(s.stepToolCalls) > 0 {
		total = append(total, s.stepToolCalls...)
	}
	return total
}

// Response returns the full Agent response once the stream has completed all ReAct iterations.
// Returns ErrStreamNotFinished if called before the stream reaches EOF.
func (s *StreamResponse) Response() (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.completed {
		return nil, ErrStreamNotFinished
	}

	text := s.stepAccumulated.String()
	toolCalls := s.allToolCalls
	if len(toolCalls) == 0 && len(s.stepToolCalls) > 0 {
		toolCalls = s.stepToolCalls
	}

	return &Response{
		Text:         text,
		Thinking:     s.stepThinking.String(),
		Content:      []llm.Part{llm.TextPart(text)},
		ToolCalls:    toolCalls,
		Messages:     s.messages,
		FinishReason: s.finishReason,
		Usage:        s.usage,
	}, nil
}

// Drain reads all remaining chunks until the stream terminates and returns the accumulated Response.
func (s *StreamResponse) Drain() (*Response, error) {
	for {
		_, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return s.Response()
}

// All returns a range-over-func iterator for reading chunks using for ... range loops.
func (s *StreamResponse) All() iter.Seq2[*llm.StreamChunk, error] {
	return func(yield func(*llm.StreamChunk, error) bool) {
		for {
			chunk, err := s.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

// accumulateToolCalls merges incoming tool call chunks or argument deltas into existing tool calls.
func accumulateToolCalls(existing, incoming []llm.ToolCall) []llm.ToolCall {
	for _, in := range incoming {
		found := false
		for i := range existing {
			if (in.ID != "" && existing[i].ID == in.ID) || (in.ID == "" && in.Name != "" && existing[i].Name == in.Name) {
				if in.Name != "" && existing[i].Name == "" {
					existing[i].Name = in.Name
				}
				existing[i].Arguments += in.Arguments
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, in)
		}
	}
	return existing
}
