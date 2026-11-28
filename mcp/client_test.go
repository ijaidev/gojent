package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConnectListsToolsAcrossPages(t *testing.T) {
	server := sdk.NewServer(
		&sdk.Implementation{Name: "test", Version: "1"},
		&sdk.ServerOptions{PageSize: 1},
	)
	addTextTool(server, "echo", "Echo text.", echoHandler)
	addTextTool(server, "second", "The second tool.", echoHandler)
	addTextTool(server, "third", "The third tool.", echoHandler)

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	statuses, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusConnected {
		t.Fatalf("status = %q, want connected: %v", statuses[0].Status, statuses[0].Err)
	}
	if statuses[0].ProtocolVersion != "2026-07-28" {
		t.Fatalf("protocol = %q, want 2026-07-28", statuses[0].ProtocolVersion)
	}

	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	want := []string{"test_echo", "test_second", "test_third"}
	if got := toolNames(tools); !equalStrings(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}

	echo := findTool(t, tools, "test_echo")
	if schemaType := echo.Spec().InputSchema["type"]; schemaType != "object" {
		t.Fatalf("schema type = %v, want object", schemaType)
	}
	result, err := echo.Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "test_echo",
		Arguments: json.RawMessage(`{"text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Text() != "hello" {
		t.Fatalf("result text = %q, want hello", result.Text())
	}
}

func TestToolErrorResult(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "fail",
		Description: "Always fails.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "boom"}},
		}, nil
	})

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	_, err = findTool(t, tools, "test_fail").Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "test_fail",
		Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrToolError) {
		t.Fatalf("error = %v, want ErrToolError", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %q, want the server text", err)
	}
}

func TestUnsupportedContentIsExplicit(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "image",
		Description: "Returns an image.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.ImageContent{Data: []byte{1, 2, 3}, MIMEType: "image/png"}},
		}, nil
	})

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	_, err = findTool(t, tools, "test_image").Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "test_image",
		Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("error = %v, want ErrUnsupportedContent", err)
	}
}

func TestToolNameCollision(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	addTextTool(server, "same.tool", "First.", echoHandler)
	addTextTool(server, "same_tool", "Second.", echoHandler)

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	_, err := client.Tools()
	if !errors.Is(err, ErrToolNameCollision) {
		t.Fatalf("error = %v, want ErrToolNameCollision", err)
	}
	if !strings.Contains(err.Error(), "test_same_tool") {
		t.Fatalf("error = %q, want the colliding name", err)
	}
}

func TestToolNamesNormalize(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	addTextTool(server, "Hello", "Uppercase name.", echoHandler)
	addTextTool(server, "a.b", "Dotted name.", echoHandler)

	client := newClient(inMemoryDial(map[string]*sdk.Server{"Test Server": server}))
	t.Cleanup(func() { _ = client.Close() })
	statuses, err := client.Connect(context.Background(), []ServerConfig{{Name: "Test Server", Command: "unused"}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if statuses[0].Status != StatusConnected {
		t.Fatalf("status = %q: %v", statuses[0].Status, statuses[0].Err)
	}

	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	want := []string{"test_server_hello", "test_server_a_b"}
	if got := toolNames(tools); !equalStrings(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
}

func TestMultipleServersIsolateFailures(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "good", Version: "1"}, nil)
	addTextTool(server, "echo", "Echo text.", echoHandler)

	dial := inMemoryDial(map[string]*sdk.Server{"good": server})
	dial = failingDial(dial, "bad", errors.New("cannot start"))
	client := newClient(dial)
	t.Cleanup(func() { _ = client.Close() })

	statuses, err := client.Connect(context.Background(), []ServerConfig{
		{Name: "bad", Command: "unused"},
		{Name: "good", Command: "unused"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2", len(statuses))
	}
	if statuses[0].Status != StatusFailed {
		t.Fatalf("bad status = %q, want failed", statuses[0].Status)
	}
	if statuses[0].Err == nil {
		t.Fatal("bad status error is nil")
	}
	if statuses[1].Status != StatusConnected {
		t.Fatalf("good status = %q, want connected: %v", statuses[1].Status, statuses[1].Err)
	}

	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if got := toolNames(tools); !equalStrings(got, []string{"good_echo"}) {
		t.Fatalf("tool names = %v, want [good_echo]", got)
	}
	if _, err := findTool(t, tools, "good_echo").Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "good_echo",
		Arguments: json.RawMessage(`{"text":"still works"}`),
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestContextCancellationAndReuse(t *testing.T) {
	started := make(chan struct{})
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "wait",
		Description: "Waits for cancellation.",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	addTextTool(server, "echo", "Echo text.", echoHandler)

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waitTool := findTool(t, tools, "test_wait")
	done := make(chan error, 1)
	go func() {
		_, err := waitTool.Execute(ctx, tool.Call{
			ID:        "call-1",
			Name:      "test_wait",
			Arguments: json.RawMessage(`{}`),
		})
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("wait handler did not start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call did not return after cancellation")
	}

	result, err := findTool(t, tools, "test_echo").Execute(context.Background(), tool.Call{
		ID:        "call-2",
		Name:      "test_echo",
		Arguments: json.RawMessage(`{"text":"reused"}`),
	})
	if err != nil {
		t.Fatalf("execute after cancellation: %v", err)
	}
	if result.Text() != "reused" {
		t.Fatalf("result = %q, want reused", result.Text())
	}
}

func TestCloseReleasesTools(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	addTextTool(server, "echo", "Echo text.", echoHandler)

	client := newClient(inMemoryDial(map[string]*sdk.Server{"test": server}))
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "test", Command: "unused"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	echo := findTool(t, tools, "test_echo")

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := client.Tools(); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("tools error = %v, want ErrClientClosed", err)
	}
	if _, err := echo.Execute(context.Background(), tool.Call{ID: "call-1", Name: "test_echo"}); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("execute error = %v, want ErrClientClosed", err)
	}
}

func TestConnectValidation(t *testing.T) {
	client := newClient(func(context.Context, ServerConfig) (session, error) {
		t.Fatal("dial called for invalid config")
		return nil, nil
	})
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: " ", Command: "unused"}}); err == nil {
		t.Fatal("missing name succeeded")
	}
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "a", Command: " "}}); err == nil {
		t.Fatal("missing command succeeded")
	}
	if _, err := client.Connect(context.Background(), []ServerConfig{{Name: "a", Command: "unused", Env: []string{"NO_EQUALS"}}}); err == nil {
		t.Fatal("invalid env succeeded")
	}
	if _, err := client.Connect(context.Background(), []ServerConfig{
		{Name: "a", Command: "unused"},
		{Name: "A", Command: "unused"},
	}); err == nil {
		t.Fatal("duplicate normalized names succeeded")
	}
}

func addTextTool(server *sdk.Server, name, description string, handler sdk.ToolHandler) {
	server.AddTool(&sdk.Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}, handler)
}

func echoHandler(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
		return nil, err
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: input.Text}},
	}, nil
}

func inMemoryDial(servers map[string]*sdk.Server) func(context.Context, ServerConfig) (session, error) {
	return func(ctx context.Context, config ServerConfig) (session, error) {
		server := servers[config.Name]
		if server == nil {
			return nil, fmt.Errorf("no test server %q", config.Name)
		}
		serverTransport, clientTransport := sdk.NewInMemoryTransports()
		go func() {
			_ = server.Run(ctx, serverTransport)
		}()
		client := sdk.NewClient(
			&sdk.Implementation{Name: "test-client", Version: "1"},
			&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}},
		)
		return client.Connect(ctx, clientTransport, nil)
	}
}

func failingDial(next func(context.Context, ServerConfig) (session, error), name string, err error) func(context.Context, ServerConfig) (session, error) {
	return func(ctx context.Context, config ServerConfig) (session, error) {
		if config.Name == name {
			return nil, err
		}
		return next(ctx, config)
	}
}

func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, candidate := range tools {
		if candidate.Spec().Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not found in %v", name, toolNames(tools))
	return nil
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		names = append(names, candidate.Spec().Name)
	}
	return names
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
