package agent

import (
	"context"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

// Run is an asynchronous agent execution.
//
// Consume Events, wait for the final response with Wait, or both. Wait does
// not drain Events, so a consumer that ignores Events and never cancels the
// run may leave the event dispatcher blocked; use a buffered consumer or call
// Cancel when abandoning a run.
type Run struct {
	events <-chan Event
	cancel context.CancelCauseFunc
	done   chan struct{}

	mu       sync.RWMutex
	response *Response
	err      error
}

// Events returns the run's event stream. The channel closes after RunEnd.
func (r *Run) Events() <-chan Event {
	return r.events
}

// Wait blocks until the run finishes or ctx is done. It does not consume
// Events.
func (r *Run) Wait(ctx context.Context) (*Response, error) {
	select {
	case <-r.done:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.response, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Response returns the final response if the run has finished.
func (r *Run) Response() (*Response, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.response, r.err
}

// Cancel stops the run with the given cause. A nil cause uses
// ErrRunCanceled.
func (r *Run) Cancel(cause error) {
	if cause == nil {
		cause = ErrRunCanceled
	}
	r.cancel(cause)
}

// Close cancels the run. It implements io.Closer for convenience.
func (r *Run) Close() error {
	r.Cancel(ErrRunCanceled)
	return nil
}

// Start begins a run asynchronously.
func (r *Runner) Start(ctx context.Context, a *Agent, s *session.Session, input ...llm.Message) (*Run, error) {
	return r.StartWith(ctx, a, s, RunOptions{}, input...)
}

// StartWith begins a run with per-run options.
func (r *Runner) StartWith(ctx context.Context, a *Agent, s *session.Session, opts RunOptions, input ...llm.Message) (*Run, error) {
	if r == nil {
		r = &Runner{}
	}
	if a == nil {
		return nil, ErrAgentRequired
	}
	model := opts.Model
	if model == nil {
		model = a.Model()
	}
	if model == nil {
		return nil, ErrModelRequired
	}
	if s == nil {
		s = opts.Session
	}
	if r.Store != nil {
		if s == nil {
			return nil, ErrSessionRequired
		}
		if s.ID == "" {
			return nil, ErrSessionIDRequired
		}
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	queue := newEventQueue(runCtx)
	runner := *r
	runner.Sink = multiSink{r.Sink, queue}

	run := &Run{
		events: queue.out,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(run.done)
		response, err := runner.RunWith(runCtx, a, s, opts, input...)
		run.mu.Lock()
		run.response = response
		run.err = err
		run.mu.Unlock()
		queue.Close()
	}()
	return run, nil
}

// Stream runs the agent asynchronously and returns its Run handle.
func (a *Agent) Stream(ctx context.Context, prompt string, opts ...Option) (*Run, error) {
	return a.StreamMessages(ctx, promptMessages(prompt), opts...)
}

// StreamMessages runs the agent asynchronously with explicit messages.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...Option) (*Run, error) {
	cfg := resolveOptions(opts)
	runner := cfg.runner
	return runner.StartWith(ctx, a, cfg.session, cfg.run, messages...)
}

func promptMessages(prompt string) []llm.Message {
	if prompt == "" {
		return nil
	}
	return []llm.Message{llm.UserMessage(prompt)}
}

// eventQueue provides an unbounded event buffer so a slow consumer never
// blocks the loop.
type eventQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []Event
	closed bool
	out    chan Event
	ctx    context.Context
}

func newEventQueue(ctx context.Context) *eventQueue {
	queue := &eventQueue{
		out: make(chan Event),
		ctx: ctx,
	}
	queue.cond = sync.NewCond(&queue.mu)
	go queue.dispatch()
	return queue
}

// Emit implements Sink.
func (q *eventQueue) Emit(event Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, event)
		q.cond.Signal()
	}
	q.mu.Unlock()
}

// Close asks the dispatcher to drain remaining events and close the channel.
func (q *eventQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Signal()
	q.mu.Unlock()
}

func (q *eventQueue) dispatch() {
	defer close(q.out)
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.cond.Wait()
		}
		if len(q.items) == 0 && q.closed {
			q.mu.Unlock()
			return
		}
		event := q.items[0]
		q.items[0] = nil
		q.items = q.items[1:]
		q.mu.Unlock()

		select {
		case q.out <- event:
		case <-q.ctx.Done():
			return
		}
	}
}
