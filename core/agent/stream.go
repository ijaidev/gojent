package agent

import (
	"context"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
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

// start begins a run asynchronously.
func (r *runner) start(ctx context.Context, a *Agent, input ...llm.Message) (*Run, error) {
	if err := r.validate(a); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	queue := newEventQueue(runCtx)
	opts := r.opts
	opts.Sink = multiSink{r.opts.Sink, queue}

	run := &Run{
		events: queue.out,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(run.done)
		response, err := newRunner(opts).run(runCtx, a, input...)
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

// StreamWith runs the agent asynchronously with a complete RunOptions value.
func (a *Agent) StreamWith(ctx context.Context, prompt string, opts RunOptions) (*Run, error) {
	return a.StreamMessagesWith(ctx, promptMessages(prompt), opts)
}

// StreamMessages runs the agent asynchronously with explicit messages.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...Option) (*Run, error) {
	return a.StreamMessagesWith(ctx, messages, resolveOptions(opts))
}

// StreamMessagesWith runs the agent asynchronously with explicit messages and
// RunOptions.
func (a *Agent) StreamMessagesWith(ctx context.Context, messages []llm.Message, opts RunOptions) (*Run, error) {
	return newRunner(opts).start(ctx, a, messages...)
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
