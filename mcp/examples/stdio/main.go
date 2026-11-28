// Command stdio demonstrates consuming a local stdio MCP server with the
// Gojent MCP client.
//
// Run it from the mcp module:
//
//	go run ./examples/stdio
//
// The command is self-contained: it launches itself in server mode, connects
// over stdio with an explicit environment allowlist, lists the server's tools,
// calls the echo tool, and prints the mapped result.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if os.Getenv("GOJENT_MCP_EXAMPLE_SERVER") == "1" {
		serve()
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := mcp.New()
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()

	statuses, err := client.Connect(ctx, []mcp.ServerConfig{{
		Name:    "demo",
		Command: executable,
		Env:     []string{"GOJENT_MCP_EXAMPLE_SERVER=1"},
	}})
	if err != nil {
		return err
	}
	for _, status := range statuses {
		fmt.Printf("server %s: %s (%s)\n", status.Name, status.Status, status.ProtocolVersion)
		if status.Status != mcp.StatusConnected {
			if status.Err != nil {
				return status.Err
			}
			return fmt.Errorf("server %s did not connect", status.Name)
		}
	}

	tools, err := client.Tools()
	if err != nil {
		return err
	}
	fmt.Println("tools:")
	for _, candidate := range tools {
		spec := candidate.Spec()
		fmt.Printf("- %s: %s\n", spec.Name, spec.Description)
	}

	echo := findTool(tools, "demo_echo")
	if echo == nil {
		return errors.New("the demo server did not expose demo_echo")
	}
	fmt.Printf("calling %s...\n", echo.Spec().Name)
	result, err := echo.Execute(ctx, tool.Call{
		ID:        "example-call",
		Name:      echo.Spec().Name,
		Arguments: json.RawMessage(`{"text":"hello from a local stdio MCP server"}`),
	})
	if err != nil {
		return err
	}
	fmt.Printf("result: %s\n", result.Text())
	return nil
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, candidate := range tools {
		if candidate.Spec().Name == name {
			return candidate
		}
	}
	return nil
}

func serve() {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "gojent-mcp-example",
		Version: "1",
	}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Returns the input text unchanged.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		},
	}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: input.Text}},
		}, nil
	})

	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
