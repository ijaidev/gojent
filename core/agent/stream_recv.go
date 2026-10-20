package agent

import (
	"errors"
	"io"

	"github.com/ijaidev/gojent/core/llm"
)

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
