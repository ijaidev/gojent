package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("GOJENT_MCP_TEST_HELPER") == "1" {
		os.Exit(runHelperProcess())
	}
	os.Exit(m.Run())
}

func runHelperProcess() int {
	if path := os.Getenv("GOJENT_MCP_TEST_SENTINEL"); path != "" {
		defer func() {
			_ = os.WriteFile(path, []byte("exited"), 0o600)
		}()
	}

	server := sdk.NewServer(
		&sdk.Implementation{Name: "gojent-mcp-test-helper", Version: "1"},
		&sdk.ServerOptions{PageSize: 1},
	)
	addTextTool(server, "echo", "Echo text.", echoHandler)
	server.AddTool(&sdk.Tool{
		Name:        "env",
		Description: "Read an environment variable.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: os.Getenv(input.Name)}},
		}, nil
	})
	server.AddTool(&sdk.Tool{
		Name:        "context",
		Description: "Report the child working directory and arguments.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		directory, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		text := directory + "\n" + strings.Join(os.Args[1:], " ")
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: text}},
		}, nil
	})
	server.AddTool(&sdk.Tool{
		Name:        "fail",
		Description: "Always fails.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "stdio boom"}},
		}, nil
	})

	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func TestStdioConnectCallAndEnvAllowlist(t *testing.T) {
	t.Setenv("GOJENT_MCP_TEST_HOST_SECRET", "leak")

	client := New()
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	dir := t.TempDir()
	statuses, err := client.Connect(context.Background(), []ServerConfig{{
		Name:    "helper",
		Command: os.Args[0],
		Args:    []string{"example-arg"},
		Dir:     dir,
		Env: []string{
			"GOJENT_MCP_TEST_HELPER=1",
			"GOJENT_MCP_TEST_ALLOWED=visible",
		},
	}})
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
	want := []string{"helper_context", "helper_echo", "helper_env", "helper_fail"}
	if got := toolNames(tools); !equalStrings(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}

	result, err := findTool(t, tools, "helper_context").Execute(context.Background(), tool.Call{
		ID:        "call-0",
		Name:      "helper_context",
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("execute context: %v", err)
	}
	if !strings.Contains(result.Text(), "example-arg") {
		t.Fatalf("child args = %q, want example-arg", result.Text())
	}
	if !strings.Contains(result.Text(), dir) {
		t.Fatalf("child dir = %q, want %q", result.Text(), dir)
	}

	result, err = findTool(t, tools, "helper_echo").Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "helper_echo",
		Arguments: json.RawMessage(`{"text":"hello over stdio"}`),
	})
	if err != nil {
		t.Fatalf("execute echo: %v", err)
	}
	if result.Text() != "hello over stdio" {
		t.Fatalf("result = %q, want hello over stdio", result.Text())
	}

	result, err = findTool(t, tools, "helper_env").Execute(context.Background(), tool.Call{
		ID:        "call-2",
		Name:      "helper_env",
		Arguments: json.RawMessage(`{"name":"GOJENT_MCP_TEST_ALLOWED"}`),
	})
	if err != nil {
		t.Fatalf("execute allowed env: %v", err)
	}
	if result.Text() != "visible" {
		t.Fatalf("allowed env = %q, want visible", result.Text())
	}

	result, err = findTool(t, tools, "helper_env").Execute(context.Background(), tool.Call{
		ID:        "call-3",
		Name:      "helper_env",
		Arguments: json.RawMessage(`{"name":"GOJENT_MCP_TEST_HOST_SECRET"}`),
	})
	if err != nil {
		t.Fatalf("execute secret env: %v", err)
	}
	if result.Text() != "" {
		t.Fatalf("host secret leaked into the child: %q", result.Text())
	}

	_, err = findTool(t, tools, "helper_fail").Execute(context.Background(), tool.Call{
		ID:        "call-4",
		Name:      "helper_fail",
		Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrToolError) {
		t.Fatalf("error = %v, want ErrToolError", err)
	}
}

func TestStdioCloseReleasesProcess(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "exited")
	client := New()
	if _, err := client.Connect(context.Background(), []ServerConfig{{
		Name:    "helper",
		Command: os.Args[0],
		Env: []string{
			"GOJENT_MCP_TEST_HELPER=1",
			"GOJENT_MCP_TEST_SENTINEL=" + sentinel,
		},
	}}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("subprocess did not run its exit path: %v", err)
	}
	if string(data) != "exited" {
		t.Fatalf("sentinel = %q, want exited", data)
	}
	if _, err := client.Tools(); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("tools error = %v, want ErrClientClosed", err)
	}
}

func TestStdioCanceledCall(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "exited")
	client := New()
	if _, err := client.Connect(context.Background(), []ServerConfig{{
		Name:    "helper",
		Command: os.Args[0],
		Env: []string{
			"GOJENT_MCP_TEST_HELPER=1",
			"GOJENT_MCP_TEST_SENTINEL=" + sentinel,
		},
	}}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = findTool(t, tools, "helper_echo").Execute(ctx, tool.Call{
		ID:        "call-1",
		Name:      "helper_echo",
		Arguments: json.RawMessage(`{"text":"never sent"}`),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("subprocess did not exit after close: %v", err)
	}
}

func TestStdioConnectFailure(t *testing.T) {
	client := New()
	t.Cleanup(func() { _ = client.Close() })

	statuses, err := client.Connect(context.Background(), []ServerConfig{{
		Name:    "missing",
		Command: filepath.Join(t.TempDir(), "does-not-exist"),
	}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusFailed {
		t.Fatalf("status = %q, want failed", statuses[0].Status)
	}
	if statuses[0].Err == nil {
		t.Fatal("failed status error is nil")
	}
	tools, err := client.Tools()
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("tools = %d, want 0", len(tools))
	}
}
