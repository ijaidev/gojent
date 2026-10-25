package agent

import (
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// Event is emitted by a run. Events are observations only; they cannot change
// execution.
type Event interface {
	event()
}

// RunStart is emitted once at the beginning of a run.
type RunStart struct {
	RunID   string
	Agent   string
	Session *session.Session
	At      time.Time
}

// StepStart is emitted before each model call.
type StepStart struct {
	Step int
}

// MessageStart is emitted before streamed assistant output.
type MessageStart struct {
	Step int
}

// TextDelta is emitted for each text fragment. Replace means Text is a full
// snapshot of the part, not an increment.
type TextDelta struct {
	Step    int
	Index   int
	Text    string
	Replace bool
}

// ThinkingDelta is emitted for each reasoning fragment. Replace means Text is a
// full snapshot of the part, not an increment.
type ThinkingDelta struct {
	Step    int
	Index   int
	Text    string
	Replace bool
}

// ToolCallDelta is emitted for each tool-call fragment.
type ToolCallDelta struct {
	Step      int
	Index     int
	ID        string
	Name      string
	Arguments string
}

// MessageEnd is emitted with the completed assistant message.
type MessageEnd struct {
	Step    int
	Message *llm.Message
}

// ToolStart is emitted before a tool call is evaluated.
type ToolStart struct {
	Step int
	Call tool.Call
	Spec tool.Spec
}

// ToolUpdate reports tool progress. The current runner does not emit it;
// it is reserved for tools and schedulers that surface progress events.
type ToolUpdate struct {
	CallID   string
	Progress string
}

// ToolEnd is emitted exactly once for every ToolStart, including denied,
// failed, or policy-aborted calls.
type ToolEnd struct {
	Step    int
	Call    tool.Call
	Result  *tool.Result
	Message *llm.Message
	Err     error
}

// StepEnd is emitted after a step's model call and tool execution.
type StepEnd struct {
	Step  int
	Usage llm.Usage
	Err   error
}

// RunEnd is always the final event. Response may be non-nil with an error when
// a run stops at a budget boundary.
type RunEnd struct {
	Response *Response
	Err      error
}

func (RunStart) event()      {}
func (StepStart) event()     {}
func (MessageStart) event()  {}
func (TextDelta) event()     {}
func (ThinkingDelta) event() {}
func (ToolCallDelta) event() {}
func (MessageEnd) event()    {}
func (ToolStart) event()     {}
func (ToolUpdate) event()    {}
func (ToolEnd) event()       {}
func (StepEnd) event()       {}
func (RunEnd) event()        {}

// Sink observes run events. Emit must not block indefinitely; use a buffered
// channel or queue when forwarding events elsewhere.
type Sink interface {
	Emit(Event)
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(Event)

// Emit implements Sink.
func (f SinkFunc) Emit(event Event) {
	f(event)
}

// multiSink fans events out to every non-nil sink in order. A panicking sink
// must not prevent later sinks, especially the run's own event queue, from
// observing the event.
type multiSink []Sink

func (s multiSink) Emit(event Event) {
	for _, sink := range s {
		if sink == nil {
			continue
		}
		safeEmit(sink, event)
	}
}
