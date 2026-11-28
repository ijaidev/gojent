package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// ErrToolError reports a server tool result with isError set. The error text
	// contains the server's text content so the model can correct itself.
	ErrToolError = errors.New("mcp: server reported a tool error")
	// ErrUnsupportedContent reports content the tracer cannot map to a Gojent
	// part. Content is never dropped silently.
	ErrUnsupportedContent = errors.New("mcp: unsupported tool content")
)

var errInvalidToolName = errors.New("mcp: invalid tool name")

var _ tool.Tool = (*serverTool)(nil)

// serverTool adapts one MCP tool to the Gojent tool contract. It deliberately
// does not use tool.BaseTool: MCP servers validate their own inputs with the
// full JSON Schema dialect, so the adapter must not apply Gojent's subset
// validator.
type serverTool struct {
	client     *Client
	server     string
	name       string
	definition *sdk.Tool
	schema     map[string]any
}

// Spec returns the tool description exposed to the model.
func (t *serverTool) Spec() tool.Spec {
	if t == nil || t.definition == nil {
		return tool.Spec{}
	}
	return tool.Spec{
		Name:        t.name,
		Description: t.definition.Description,
		InputSchema: cloneSchema(t.schema),
	}
}

// Execute sends the call to the owning server and maps its result.
func (t *serverTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if t == nil || t.client == nil {
		return tool.Result{}, errors.New("mcp: tool is not configured")
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("mcp: tool %s: %w", t.name, err)
	}

	sess, err := t.client.session(t.server)
	if err != nil {
		return tool.Result{}, fmt.Errorf("mcp: tool %s: %w", t.name, err)
	}
	arguments, err := decodeArguments(call.Arguments)
	if err != nil {
		return tool.Result{}, fmt.Errorf("mcp: tool %s: %w", t.name, err)
	}

	result, err := sess.CallTool(ctx, &sdk.CallToolParams{
		Name:      t.definition.Name,
		Arguments: arguments,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tool.Result{}, fmt.Errorf("mcp: tool %s: %w", t.name, ctxErr)
		}
		return tool.Result{}, fmt.Errorf("mcp: tool %s: %w", t.name, err)
	}
	mapped, err := mapResult(result)
	if err != nil {
		return mapped, fmt.Errorf("mcp: tool %s: %w", t.name, err)
	}
	return mapped, nil
}

func decodeArguments(raw json.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var arguments any
	if err := decoder.Decode(&arguments); err != nil {
		return nil, fmt.Errorf("%w: decode arguments: %w", tool.ErrInvalidInput, err)
	}
	if arguments == nil {
		return map[string]any{}, nil
	}
	if _, ok := arguments.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: arguments must be a JSON object", tool.ErrInvalidInput)
	}
	return arguments, nil
}

func mapResult(result *sdk.CallToolResult) (tool.Result, error) {
	if result == nil {
		return tool.Result{}, nil
	}

	parts := make([]llm.Part, 0, len(result.Content))
	var unsupported error
	var text strings.Builder
	for _, content := range result.Content {
		textContent, ok := content.(*sdk.TextContent)
		if !ok || textContent == nil {
			if unsupported == nil {
				unsupported = fmt.Errorf("%w: %T", ErrUnsupportedContent, content)
			}
			continue
		}
		if textContent.Text == "" {
			continue
		}
		parts = append(parts, llm.NewTextPart(textContent.Text))
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString(textContent.Text)
	}

	mapped := tool.Result{Content: parts}
	if result.StructuredContent != nil {
		mapped.Data = result.StructuredContent
	}
	if result.IsError {
		message := strings.TrimSpace(text.String())
		if message == "" {
			message = "no error text was returned"
		}
		return mapped, fmt.Errorf("%w: %s", ErrToolError, message)
	}
	if unsupported != nil {
		return mapped, unsupported
	}
	return mapped, nil
}

func toolName(server, name string) (string, error) {
	serverPart := normalizeSegment(server)
	namePart := normalizeSegment(name)
	if serverPart == "" || namePart == "" {
		return "", fmt.Errorf("%w: server %q tool %q", errInvalidToolName, server, name)
	}
	return serverPart + "_" + namePart, nil
}

// normalizeSegment reduces a name to lowercase ASCII letters, digits,
// underscores, and hyphens. Runs of other characters become one underscore,
// and leading or trailing underscores are removed.
func normalizeSegment(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	lastUnderscore := false
	for _, character := range strings.ToLower(value) {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '-':
			builder.WriteRune(character)
			lastUnderscore = false
		case lastUnderscore:
			// Collapse a run of separators into the underscore already written.
		default:
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func normalizeSchema(raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{"type": "object"}, nil
	}
	if schema, ok := raw.(map[string]any); ok {
		return cloneSchema(schema), nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: encode input schema: %w", tool.ErrInvalidSchema, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, fmt.Errorf("%w: decode input schema: %w", tool.ErrInvalidSchema, err)
	}
	if schema == nil {
		return map[string]any{"type": "object"}, nil
	}
	return schema, nil
}

func cloneSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	cloned := make(map[string]any, len(schema))
	for key, value := range schema {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneSchema(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index := range typed {
			cloned[index] = cloneJSONValue(typed[index])
		}
		return cloned
	default:
		return value
	}
}
