package agent

import (
	"context"
	"sync"
)

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
