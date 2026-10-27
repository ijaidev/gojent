package anthropic

import (
	"context"

	"github.com/ijaidev/gojent/core/llm"
)

// Generate sends a non-streaming Messages request.
func (m *Model) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := m.requestParams(req)
	if err != nil {
		return nil, err
	}
	opts, err := m.headerOptions(ctx)
	if err != nil {
		return nil, err
	}

	message, err := m.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return nil, wrapError(err)
	}
	return m.canonicalResponse(message)
}

// Stream initiates a streaming Messages request.
func (m *Model) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := m.requestParams(req)
	if err != nil {
		return nil, err
	}
	opts, err := m.headerOptions(ctx)
	if err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan llm.StreamEvent, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errCh)

		sdkStream := m.client.Messages.NewStreaming(streamCtx, params, opts...)
		defer func() {
			_ = sdkStream.Close()
		}()

		state := newStreamState(streamCtx, events, m.modelID)
		for sdkStream.Next() {
			if err := state.apply(sdkStream.Current()); err != nil {
				if streamCtx.Err() != nil {
					return
				}
				sendStreamError(streamCtx, errCh, err)
				return
			}
		}
		if err := sdkStream.Err(); err != nil {
			if streamCtx.Err() != nil {
				return
			}
			sendStreamError(streamCtx, errCh, wrapError(err))
			return
		}
		if streamCtx.Err() != nil {
			return
		}
		if err := state.finish(); err != nil {
			sendStreamError(streamCtx, errCh, err)
		}
	}()

	return llm.NewChannelStream(streamCtx, cancel, events, errCh), nil
}

// sendStreamError reports a terminal error unless the consumer went away.
func sendStreamError(ctx context.Context, ch chan<- error, err error) {
	select {
	case ch <- err:
	case <-ctx.Done():
	}
}
