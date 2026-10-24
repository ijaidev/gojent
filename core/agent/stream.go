package agent

import (
	"context"
	"runtime/debug"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
)

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
	r, err := newRunner(a, opts)
	if err != nil {
		return nil, err
	}
	return r.start(ctx, messages...)
}

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

// start begins the execution asynchronously.
func (r *runner) start(ctx context.Context, input ...llm.Message) (*Run, error) {
	input = llm.CloneMessages(input)
	runCtx, cancel := context.WithCancelCause(ctx)
	queue := newEventQueue(runCtx)
	r.opts.Sink = multiSink{r.opts.Sink, queue}

	run := &Run{
		events: queue.out,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(run.done)
		defer queue.Close()
		// r.run converts panics to errors. Recover here as a last resort so a
		// panic before that guard still closes the event stream and Run.Wait.
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := &PanicError{Value: recovered, Stack: debug.Stack()}
				run.mu.Lock()
				run.response = nil
				run.err = panicErr
				run.mu.Unlock()
				safeEmit(r.opts.Sink, RunEnd{Err: panicErr})
			}
		}()

		response, err := r.run(runCtx, input...)
		run.mu.Lock()
		run.response = response
		run.err = err
		run.mu.Unlock()
	}()
	return run, nil
}

func promptMessages(prompt string) []llm.Message {
	if prompt == "" {
		return nil
	}
	return []llm.Message{llm.UserMessage(prompt)}
}
