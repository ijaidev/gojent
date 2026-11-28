package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeSession struct {
	mu           sync.Mutex
	closed       bool
	closeCalls   int
	closeErr     error
	closeStarted chan struct{}
	closeRelease chan struct{}
	startOnce    sync.Once
	list         func(context.Context) (*sdk.ListToolsResult, error)
}

func (f *fakeSession) ListTools(ctx context.Context, _ *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	if f.list != nil {
		return f.list(ctx)
	}
	return &sdk.ListToolsResult{}, nil
}

func (f *fakeSession) CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	return &sdk.CallToolResult{}, nil
}

func (f *fakeSession) InitializeResult() *sdk.InitializeResult {
	return &sdk.InitializeResult{ProtocolVersion: "2026-07-28"}
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	f.closeCalls++
	started := f.closeStarted
	release := f.closeRelease
	err := f.closeErr
	f.mu.Unlock()

	if started != nil {
		f.startOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}

	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return err
}

func (f *fakeSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeSession) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

func TestCloseWaitsForInFlightConnect(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	sess := &fakeSession{}
	client := newClient(func(context.Context, ServerConfig) (session, error) {
		close(started)
		<-release
		return sess, nil
	})

	type connectResult struct {
		statuses []ServerStatus
		err      error
	}
	connectDone := make(chan connectResult, 1)
	go func() {
		statuses, err := client.Connect(context.Background(), []ServerConfig{{
			Name:    "slow",
			Command: "unused",
		}})
		connectDone <- connectResult{statuses: statuses, err: err}
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- client.Close() }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before in-flight setup finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after setup finished")
	}

	var connected connectResult
	select {
	case connected = <-connectDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return after setup finished")
	}
	if connected.err != nil {
		t.Fatalf("connect: %v", connected.err)
	}
	if len(connected.statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(connected.statuses))
	}
	if connected.statuses[0].Status != StatusFailed {
		t.Fatalf("status = %q, want failed", connected.statuses[0].Status)
	}
	if !errors.Is(connected.statuses[0].Err, ErrClientClosed) {
		t.Fatalf("status error = %v, want ErrClientClosed", connected.statuses[0].Err)
	}
	if !sess.isClosed() {
		t.Fatal("session created during shutdown was still open when Close returned")
	}
}

func TestConcurrentCloseWaitsForShutdown(t *testing.T) {
	closeFailure := errors.New("close failed")
	sess := &fakeSession{
		closeErr:     closeFailure,
		closeStarted: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	client := newClient(func(context.Context, ServerConfig) (session, error) {
		return sess, nil
	})
	if _, err := client.Connect(context.Background(), []ServerConfig{{
		Name:    "test",
		Command: "unused",
	}}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	first := make(chan error, 1)
	go func() { first <- client.Close() }()
	<-sess.closeStarted

	second := make(chan error, 1)
	go func() { second <- client.Close() }()

	select {
	case err := <-second:
		t.Fatalf("second Close returned before shutdown completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(sess.closeRelease)
	for name, done := range map[string]chan error{"first": first, "second": second} {
		select {
		case err := <-done:
			if !errors.Is(err, closeFailure) {
				t.Fatalf("%s close error = %v, want %v", name, err, closeFailure)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s Close did not return", name)
		}
	}
	if got := sess.calls(); got != 1 {
		t.Fatalf("session Close calls = %d, want 1", got)
	}
}
