package openai

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
)

// chatStreamTool accumulates one streamed tool call. Chat Completions streams
// tool calls by index, with the ID and name arriving on the first chunk and the
// arguments split across later chunks.
type chatStreamTool struct {
	index   int
	callID  string
	name    string
	args    strings.Builder
	started bool
	ended   bool
}

// chatCompletionStream maps Chat Completions chunks onto canonical stream
// events. It pulls from the SDK stream lazily from Recv.
type chatCompletionStream struct {
	ctx    context.Context
	stream *ssestream.Stream[sdk.ChatCompletionChunk]
	model  string

	pending []llm.StreamEvent
	closed  bool
	done    bool

	started      bool
	nextPart     int
	textIndex    int
	textEnded    bool
	refusalIndex int
	refusalEnded bool
	tools        map[int64]*chatStreamTool
	toolOrder    []*chatStreamTool

	finishReason    llm.FinishReason
	rawFinishReason string
	status          llm.MessageStatus
	usage           llm.Usage
}

var _ llm.Stream = (*chatCompletionStream)(nil)

func newChatCompletionStream(
	ctx context.Context,
	stream *ssestream.Stream[sdk.ChatCompletionChunk],
	model string,
) *chatCompletionStream {
	return &chatCompletionStream{
		ctx:          ctx,
		stream:       stream,
		model:        model,
		textIndex:    -1,
		refusalIndex: -1,
		tools:        make(map[int64]*chatStreamTool),
	}
}

// Recv implements llm.Stream.
func (s *chatCompletionStream) Recv() (llm.StreamEvent, error) {
	if s.closed {
		return nil, llm.ErrStreamClosed
	}

	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			return event, nil
		}
		if s.done {
			s.closed = true
			return nil, io.EOF
		}
		if err := s.ctx.Err(); err != nil {
			s.closed = true
			return nil, err
		}

		if !s.stream.Next() {
			if err := s.stream.Err(); err != nil {
				s.closed = true
				return nil, wrapError(err)
			}
			if err := s.finish(); err != nil {
				s.closed = true
				return nil, err
			}
			s.done = true
			continue
		}

		chunk := s.stream.Current()
		if err := s.applyChunk(&chunk); err != nil {
			s.closed = true
			return nil, err
		}
	}
}

// Close implements llm.Stream.
func (s *chatCompletionStream) Close() error {
	s.closed = true
	return s.stream.Close()
}

func (s *chatCompletionStream) applyChunk(chunk *sdk.ChatCompletionChunk) error {
	if chunk == nil {
		return fmt.Errorf("%w: nil Chat Completions chunk", llm.ErrInvalidMessage)
	}
	if !s.started {
		s.started = true
		model := chunk.Model
		if model == "" {
			model = s.model
		}
		s.pending = append(s.pending, llm.MessageStart{ID: chunk.ID, Model: model})
	}
	if chunk.JSON.Usage.Valid() {
		s.usage = chatUsage(chunk.Usage)
	}

	for index := range chunk.Choices {
		choice := &chunk.Choices[index]
		// Requests always ask for a single choice.
		if choice.Index != 0 {
			continue
		}
		if choice.FinishReason != "" {
			s.rawFinishReason = choice.FinishReason
			s.finishReason, s.status = mapChatFinishReason(choice.FinishReason)
		}
		if choice.Delta.Content != "" {
			if err := s.appendText(choice.Delta.Content); err != nil {
				return err
			}
		}
		if choice.Delta.Refusal != "" {
			if err := s.appendRefusal(choice.Delta.Refusal); err != nil {
				return err
			}
		}
		for index := range choice.Delta.ToolCalls {
			if err := s.applyToolCall(&choice.Delta.ToolCalls[index]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *chatCompletionStream) appendText(text string) error {
	if s.textEnded {
		return fmt.Errorf("%w: text delta after the text part ended", llm.ErrInvalidPart)
	}
	if s.textIndex < 0 {
		s.textIndex = s.reservePart()
		s.pending = append(s.pending, llm.PartStart{
			Index: s.textIndex,
			Part:  llm.NewTextPart(""),
		})
	}
	s.pending = append(s.pending, llm.PartDelta{
		Index: s.textIndex,
		Kind:  llm.PartTypeText,
		Text:  text,
	})
	return nil
}

func (s *chatCompletionStream) appendRefusal(text string) error {
	if s.refusalEnded {
		return fmt.Errorf("%w: refusal delta after the refusal part ended", llm.ErrInvalidPart)
	}
	if s.refusalIndex < 0 {
		s.refusalIndex = s.reservePart()
		s.pending = append(s.pending, llm.PartStart{
			Index: s.refusalIndex,
			Part:  llm.NewRefusalPart(""),
		})
	}
	s.pending = append(s.pending, llm.PartDelta{
		Index: s.refusalIndex,
		Kind:  llm.PartTypeRefusal,
		Text:  text,
	})
	return nil
}

func (s *chatCompletionStream) applyToolCall(call *sdk.ChatCompletionChunkChoiceDeltaToolCall) error {
	state := s.tools[call.Index]
	if state == nil {
		state = &chatStreamTool{index: -1}
		s.tools[call.Index] = state
		s.toolOrder = append(s.toolOrder, state)
	}
	if state.ended {
		return fmt.Errorf("%w: tool call delta after the call ended", llm.ErrInvalidToolCall)
	}
	if call.ID != "" {
		state.callID = call.ID
	}
	if call.Function.Name != "" {
		state.name = call.Function.Name
	}
	if call.Function.Arguments != "" {
		state.args.WriteString(call.Function.Arguments)
	}

	if !state.started {
		// Some servers send arguments before the ID or name. Wait until the
		// call has both so the canonical part is valid.
		if state.callID == "" || state.name == "" {
			return nil
		}
		state.index = s.reservePart()
		state.started = true
		s.pending = append(s.pending, llm.PartStart{
			Index: state.index,
			Part: llm.NewToolCallPart(llm.ToolCall{
				CallID: state.callID,
				Name:   state.name,
			}),
		})
		if args := state.args.String(); args != "" {
			s.pending = append(s.pending, llm.PartDelta{
				Index: state.index,
				Kind:  llm.PartTypeToolCall,
				Call: &llm.ToolCallDelta{
					CallID:        state.callID,
					Name:          state.name,
					InputFragment: args,
				},
			})
		}
		return nil
	}

	if call.Function.Arguments != "" {
		s.pending = append(s.pending, llm.PartDelta{
			Index: state.index,
			Kind:  llm.PartTypeToolCall,
			Call: &llm.ToolCallDelta{
				CallID:        state.callID,
				Name:          state.name,
				InputFragment: call.Function.Arguments,
			},
		})
	}
	return nil
}

func (s *chatCompletionStream) finish() error {
	if !s.started {
		return fmt.Errorf("%w: Chat Completions stream produced no chunks", llm.ErrInvalidMessage)
	}

	hasPart := false
	if s.textIndex >= 0 {
		hasPart = true
		s.endPart(&s.textEnded, s.textIndex)
	}
	if s.refusalIndex >= 0 {
		hasPart = true
		s.endPart(&s.refusalEnded, s.refusalIndex)
	}
	for _, state := range s.toolOrder {
		if !state.started {
			return fmt.Errorf("%w: tool call %q never received an ID and name", llm.ErrInvalidToolCall, state.name)
		}
		hasPart = true
		s.endPart(&state.ended, state.index)
	}
	if !hasPart {
		return fmt.Errorf("%w: Chat Completions stream produced no content", llm.ErrInvalidMessage)
	}

	if s.finishReason == "" {
		s.finishReason = llm.FinishReasonStop
	}
	if s.status == "" {
		s.status = llm.MessageStatusCompleted
	}
	s.pending = append(s.pending, llm.MessageEnd{
		Status:          s.status,
		FinishReason:    s.finishReason,
		RawFinishReason: s.rawFinishReason,
		Usage:           s.usage,
	})
	return nil
}

func (s *chatCompletionStream) endPart(ended *bool, index int) {
	if *ended {
		return
	}
	*ended = true
	s.pending = append(s.pending, llm.PartEnd{Index: index})
}

func (s *chatCompletionStream) reservePart() int {
	index := s.nextPart
	s.nextPart++
	return index
}
