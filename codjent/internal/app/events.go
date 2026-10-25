package app

import (
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

// Event types emitted by a run.
const (
	EventRunStarted       = "run_started"
	EventTextDelta        = "text_delta"
	EventThinkingDelta    = "thinking_delta"
	EventToolCall         = "tool_call"
	EventToolResult       = "tool_result"
	EventApprovalRequired = "approval_required"
	EventApprovalResolved = "approval_resolved"
	EventRunCompleted     = "run_completed"
	EventRunFailed        = "run_failed"
	EventRunCanceled      = "run_canceled"
)

// Event is one ordered run event. The sequence ID is stable for replay.
type Event struct {
	ID    int64  `json:"id"`
	RunID string `json:"run_id"`
	Type  string `json:"type"`
	Data  any    `json:"data,omitempty"`
}

// RunStartedEvent describes the run setup.
type RunStartedEvent struct {
	ThreadID   string     `json:"thread_id"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	AccessMode AccessMode `json:"access_mode"`
}

// TextDeltaEvent is a chunk of assistant text. Index is the canonical part
// index.
type TextDeltaEvent struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// ThinkingDeltaEvent is a chunk of model reasoning. Index is the canonical part
// index.
type ThinkingDeltaEvent struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// ToolCallEvent announces a complete tool call before execution. Index is the
// canonical part index within the assistant message.
type ToolCallEvent struct {
	Index     int    `json:"index"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResultEvent reports a completed or skipped tool invocation. Index is the
// canonical part index within the assistant message.
type ToolResultEvent struct {
	Index   int    `json:"index"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// ApprovalRequiredEvent asks the client to allow or deny a tool call.
type ApprovalRequiredEvent struct {
	Approval PendingApproval `json:"approval"`
}

// ApprovalResolvedEvent reports the decision for an approval request.
type ApprovalResolvedEvent struct {
	ApprovalID string `json:"approval_id"`
	Allow      bool   `json:"allow"`
	Note       string `json:"note,omitempty"`
}

// RunCompletedEvent closes a successful run.
type RunCompletedEvent struct {
	Usage        llm.Usage        `json:"usage"`
	FinishReason llm.FinishReason `json:"finish_reason"`
	MessageIDs   []string         `json:"message_ids,omitempty"`
}

// RunFailedEvent closes a failed run.
type RunFailedEvent struct {
	Error string `json:"error"`
}

// RunCanceledEvent closes a canceled run.
type RunCanceledEvent struct {
	Reason string `json:"reason,omitempty"`
}

const (
	maxEventLog         = 20_000
	maxSubscriberBuffer = 1_024
)

// eventBus is a bounded, replayable event log with live subscribers.
type eventBus struct {
	mu      sync.Mutex
	runID   string
	nextID  int64
	events  []Event
	subs    map[uint64]chan Event
	nextSub uint64
	closed  bool
}

func newEventBus(runID string) *eventBus {
	return &eventBus{
		runID: runID,
		subs:  make(map[uint64]chan Event),
	}
}

func (b *eventBus) emit(eventType string, data any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}

	b.nextID++
	event := Event{
		ID:    b.nextID,
		RunID: b.runID,
		Type:  eventType,
		Data:  data,
	}
	b.events = append(b.events, event)
	if len(b.events) > maxEventLog {
		b.events = append([]Event(nil), b.events[len(b.events)-maxEventLog:]...)
	}

	for id, ch := range b.subs {
		select {
		case ch <- event:
		default:
			// A slow subscriber can reconnect with ?after=<last seen id>.
			delete(b.subs, id)
			close(ch)
		}
	}
}

// subscribe returns replayable events after the given ID and a live channel.
// The returned cancel function is idempotent.
func (b *eventBus) subscribe(after int64) (backlog []Event, live <-chan Event, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, event := range b.events {
		if event.ID > after {
			backlog = append(backlog, event)
		}
	}

	if b.closed {
		closed := make(chan Event)
		close(closed)
		return backlog, closed, func() {}
	}

	b.nextSub++
	id := b.nextSub
	subscriber := make(chan Event, maxSubscriberBuffer)
	b.subs[id] = subscriber

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if existing, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(existing)
			}
		})
	}
	return backlog, subscriber, cancel
}

func (b *eventBus) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
