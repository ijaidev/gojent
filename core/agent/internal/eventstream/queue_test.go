package eventstream_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/agent/internal/eventstream"
)

func TestQueueDeliversInOrderAndCloses(t *testing.T) {
	queue := eventstream.New[int](context.Background())
	queue.Emit(1)
	queue.Emit(2)
	queue.Emit(3)
	queue.Close()

	var got []int
	for value := range queue.Events() {
		got = append(got, value)
	}
	if want := []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

func TestEmitAfterCloseIsIgnored(t *testing.T) {
	queue := eventstream.New[int](context.Background())
	queue.Close()
	queue.Emit(1)

	select {
	case value, ok := <-queue.Events():
		if ok {
			t.Fatalf("received %d from a closed queue", value)
		}
	case <-time.After(time.Second):
		t.Fatal("Events did not close")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	queue := eventstream.New[int](context.Background())
	queue.Emit(1)
	queue.Close()
	queue.Close()

	var got []int
	for value := range queue.Events() {
		got = append(got, value)
	}
	if want := []int{1}; !slices.Equal(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

func TestEmitDoesNotBlockWithoutConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	queue := eventstream.New[int](ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 10_000 {
			queue.Emit(i)
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Emit blocked without a consumer")
	}
	queue.Close()
}

func TestCancelClosesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := eventstream.New[int](ctx)
	cancel()

	select {
	case _, ok := <-queue.Events():
		if ok {
			t.Fatal("received a value from a canceled queue")
		}
	case <-time.After(time.Second):
		t.Fatal("Events did not close after cancellation")
	}
}

func TestConcurrentEmitDoesNotLoseValues(t *testing.T) {
	const (
		producers   = 8
		perProducer = 100
	)
	queue := eventstream.New[int](context.Background())

	var wg sync.WaitGroup
	for producer := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perProducer {
				queue.Emit(producer*perProducer + i)
			}
		}()
	}
	wg.Wait()
	queue.Close()

	count := 0
	for range queue.Events() {
		count++
	}
	if want := producers * perProducer; count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}
