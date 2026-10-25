package agent

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// PanicError converts a recovered panic into an error.
type PanicError struct {
	Value any
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	if e == nil {
		return "agent: panic: <nil>"
	}
	return fmt.Sprintf("agent: panic: %v", e.Value)
}

// safeEmit protects the run from a sink that panics.
func safeEmit(sink Sink, event Event) {
	if sink == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	sink.Emit(event)
}

// guardErr converts a panic in a dependency into a PanicError.
func guardErr(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// guardValue converts a panic in a dependency into a PanicError, returning
// the zero value alongside it.
func guardValue[T any](fn func() (T, error)) (value T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			var zero T
			value = zero
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return fn()
}

func callPolicy(fn func() error) error {
	return guardErr(fn)
}

func callApprove(fn func() (bool, error)) (bool, error) {
	return guardValue(fn)
}

func callGenerate(ctx context.Context, model llm.Model, request *llm.Request) (*llm.Response, error) {
	return guardValue(func() (*llm.Response, error) {
		return model.Generate(ctx, request)
	})
}

func callStream(ctx context.Context, streamer llm.Streamer, request *llm.Request) (llm.Stream, error) {
	return guardValue(func() (llm.Stream, error) {
		return streamer.Stream(ctx, request)
	})
}

func recvStream(stream llm.Stream) (*llm.StreamChunk, error) {
	return guardValue(stream.Recv)
}

func callTool(ctx context.Context, impl tool.Tool, call tool.Call) (tool.Result, error) {
	return guardValue(func() (tool.Result, error) {
		return impl.Execute(ctx, call)
	})
}
