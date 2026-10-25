// Package eventstream provides a non-blocking event queue.
package eventstream

import (
	"context"
	"sync"
)

// Queue is an unbounded event buffer so a slow consumer never blocks the
// producer. One dispatcher goroutine forwards buffered values to Events in
// order and closes the channel when the queue is closed or ctx is done.
//
// Queue is safe for concurrent use. Emit never blocks. Close drains the
// remaining buffered values before closing Events. Emitting after Close is a
// no-op.
type Queue[T any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []T
	closed bool
	out    chan T
	ctx    context.Context
	done   chan struct{}
}

// New starts a queue that stops dispatching when ctx is done.
func New[T any](ctx context.Context) *Queue[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	queue := &Queue[T]{
		out:  make(chan T),
		ctx:  ctx,
		done: make(chan struct{}),
	}
	queue.cond = sync.NewCond(&queue.mu)
	go queue.dispatch()
	go queue.watch()
	return queue
}

// Events returns the channel of buffered values. It closes after the queue is
// closed and drained, or when the queue's context is done.
func (q *Queue[T]) Events() <-chan T {
	return q.out
}

// Emit buffers value. It never blocks and is a no-op after Close.
func (q *Queue[T]) Emit(value T) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, value)
		q.cond.Signal()
	}
	q.mu.Unlock()
}

// Close stops accepting values and asks the dispatcher to drain the buffer
// before closing Events.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Signal()
	q.mu.Unlock()
}

// watch closes the queue when ctx is done so cancellation is observed even
// while the dispatcher is waiting for its first value.
func (q *Queue[T]) watch() {
	select {
	case <-q.ctx.Done():
		q.Close()
	case <-q.done:
	}
}

func (q *Queue[T]) dispatch() {
	defer close(q.done)
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
		value := q.items[0]
		var zero T
		q.items[0] = zero
		q.items = q.items[1:]
		q.mu.Unlock()

		select {
		case q.out <- value:
		case <-q.ctx.Done():
			return
		}
	}
}
