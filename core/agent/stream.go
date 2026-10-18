package agent

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

var (
	// ErrStreamNotFinished is returned when accessing the final agent response before the stream completes.
	ErrStreamNotFinished = errors.New("agent: stream has not finished yet")
	// ErrStreamClosed is returned when operations are attempted on a closed stream.
	ErrStreamClosed = errors.New("agent: stream is closed")
)

// StreamResponse manages the streaming lifecycle of an agent response,
// executing tools across ReAct iterations and accumulating tokens, deltas,
// and messages in real time.
type StreamResponse struct {
	mu              sync.Mutex
	ctx             context.Context
	agent           *Agent
	genCfg          *GenerateConfig
	stream          llm.Stream
	messages        []llm.Message
	activeTools     []tool.Tool
	toolDefs        []llm.ToolDefinition
	req             llm.GenerateRequest
	hooks           Hooks
	maxSteps        int
	step            int
	stepAccumulated strings.Builder
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
func newStreamResponse(
	ctx context.Context,
	agent *Agent,
	genCfg *GenerateConfig,
	req llm.GenerateRequest,
	stream llm.Stream,
	messages []llm.Message,
	activeTools []tool.Tool,
	toolDefs []llm.ToolDefinition,
	maxSteps int,
	hooks Hooks,
) *StreamResponse {
	return &StreamResponse{
		ctx:          ctx,
		agent:        agent,
		genCfg:       genCfg,
		stream:       stream,
		messages:     messages,
		activeTools:  activeTools,
		toolDefs:     toolDefs,
		req:          req,
		hooks:        hooks,
		maxSteps:     maxSteps,
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

			completedStep := s.step
			s.hooks.afterModel(s.ctx, completedStep, &s.req, &llm.GenerateResponse{
				Text:         s.stepAccumulated.String(),
				ToolCalls:    s.stepToolCalls,
				FinishReason: s.finishReason,
				Usage:        s.stepUsage,
				Raw:          s.stepRaw,
			}, nil)

			// If the model did not request any tools, it has reached its final answer!
			if len(s.stepToolCalls) == 0 {
				finalMsg := llm.Message{
					Role:    llm.RoleAssistant,
					Content: s.stepAccumulated.String(),
					Raw:     s.stepRaw,
				}
				s.messages = append(s.messages, finalMsg)
				s.completed = true
				if s.genCfg != nil && s.genCfg.Storage != nil && s.genCfg.ThreadID != "" {
					stored := storage.NewMessageFromLLM(s.genCfg.ThreadID, finalMsg)
					if s.genCfg.ResourceID != "" {
						stored.ResourceID = s.genCfg.ResourceID
					}
					if _, err := s.genCfg.Storage.SaveMessages(s.ctx, []*storage.Message{stored}); err != nil {
						s.hooks.storageError(s.ctx, opSaveMessages, err)
					}
				}
				return nil, io.EOF
			}

			// Model requested tool calls in this step
			assistantMsg := llm.Message{
				Role:      llm.RoleAssistant,
				Content:   s.stepAccumulated.String(),
				ToolCalls: s.stepToolCalls,
				Raw:       s.stepRaw,
			}
			s.messages = append(s.messages, assistantMsg)
			s.allToolCalls = append(s.allToolCalls, s.stepToolCalls...)
			if s.genCfg != nil && s.genCfg.Storage != nil && s.genCfg.ThreadID != "" {
				stored := storage.NewMessageFromLLM(s.genCfg.ThreadID, assistantMsg)
				if s.genCfg.ResourceID != "" {
					stored.ResourceID = s.genCfg.ResourceID
				}
				if _, err := s.genCfg.Storage.SaveMessages(s.ctx, []*storage.Message{stored}); err != nil {
					s.hooks.storageError(s.ctx, opSaveMessages, err)
				}
			}

			nextStep := completedStep + 1
			if nextStep >= s.maxSteps {
				s.step = nextStep
				s.finishReason = llm.FinishReasonLength
				s.completed = true
				return nil, io.EOF
			}

			// Execute all requested tool calls
			for _, call := range s.stepToolCalls {
				output, _ := s.agent.executeToolCall(s.ctx, completedStep, call, s.activeTools, s.hooks)
				toolMsg := llm.Message{
					Role:       llm.RoleTool,
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    output,
				}
				s.messages = append(s.messages, toolMsg)
				if s.genCfg != nil && s.genCfg.Storage != nil && s.genCfg.ThreadID != "" {
					stored := storage.NewMessageFromLLM(s.genCfg.ThreadID, toolMsg)
					if s.genCfg.ResourceID != "" {
						stored.ResourceID = s.genCfg.ResourceID
					}
					if _, err := s.genCfg.Storage.SaveMessages(s.ctx, []*storage.Message{stored}); err != nil {
						s.hooks.storageError(s.ctx, opSaveMessages, err)
					}
				}
			}

			s.step = nextStep

			// Reset step buffers
			s.stepToolCalls = nil
			s.stepAccumulated.Reset()
			s.stepUsage = llm.Usage{}
			s.stepRaw = nil

			// Prepare request for next ReAct iteration
			req := llm.GenerateRequest{
				Messages:    s.messages,
				Tools:       s.toolDefs,
				Temperature: s.genCfg.Temperature,
				MaxTokens:   s.genCfg.MaxTokens,
				TopP:        s.genCfg.TopP,
			}

			if err := s.hooks.beforeModel(s.ctx, s.step, &req); err != nil {
				s.err = err
				return nil, err
			}

			nextStream, streamErr := s.genCfg.Model.Stream(s.ctx, req)
			if streamErr != nil {
				s.hooks.afterModel(s.ctx, s.step, &req, nil, streamErr)
				s.err = streamErr
				return nil, streamErr
			}
			s.req = req
			s.stream = nextStream
			continue
		}

		if err != nil {
			s.hooks.afterModel(s.ctx, s.step, &s.req, &llm.GenerateResponse{
				Text:      s.stepAccumulated.String(),
				ToolCalls: s.stepToolCalls,
				Usage:     s.stepUsage,
				Raw:       s.stepRaw,
			}, err)
			s.err = err
			return nil, err
		}

		s.stepAccumulated.WriteString(chunk.Delta)
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
