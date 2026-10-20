package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// Tools returns a snapshot slice of all registered tools on this agent.
func (a *Agent) Tools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make([]tool.Tool, 0, len(a.tools))
	for _, t := range a.tools {
		res = append(res, t)
	}
	return res
}

// GetTool retrieves a registered tool by its unique identifier.
func (a *Agent) GetTool(id string) (tool.Tool, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	t, ok := a.tools[id]
	return t, ok
}

// AddTool registers a new tool on the agent.
func (a *Agent) AddTool(t tool.Tool) error {
	if t == nil {
		return ErrToolNil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools[t.ID()] = t
	return nil
}

// RemoveTool unregisters a tool from the agent by ID.
func (a *Agent) RemoveTool(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tools, id)
}

// executeToolCall executes a single tool call against the given tools, firing
// BeforeTool and AfterTool hooks, and returns the string output and multipart content delivered to the model.
func (a *Agent) executeToolCall(ctx context.Context, step int, call llm.ToolCall, activeTools []tool.Tool, hooks Hooks) (string, []llm.Part, error) {
	ev := &ToolEvent{Step: step, Call: &call}

	if hooks.BeforeTool != nil {
		if err := hooks.BeforeTool(ctx, ev); err != nil {
			ev.Output = fmt.Sprintf(`{"error":%q}`, err.Error())
			ev.Err = err
			ev.Skipped = true
			hooks.afterTool(ctx, ev)
			return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, err
		}
	}

	var target tool.Tool
	for _, t := range activeTools {
		if t != nil && t.ID() == call.Name {
			target = t
			break
		}
	}
	if target == nil {
		ev.Output = fmt.Sprintf(`{"error":"tool '%s' not found"}`, call.Name)
		ev.Err = fmt.Errorf("agent: tool %q not found", call.Name)
		hooks.afterTool(ctx, ev)
		return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, ev.Err
	}

	out, err := target.Execute(ctx, []byte(call.Arguments))
	if err != nil {
		ev.Output = fmt.Sprintf(`{"error":%q}`, err.Error())
		ev.Err = err
		hooks.afterTool(ctx, ev)
		return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, err
	}

	var toolParts []llm.Part
	switch v := out.(type) {
	case tool.Result:
		toolParts = v.Content
		ev.Output = v.Text()
	case *tool.Result:
		if v != nil {
			toolParts = v.Content
			ev.Output = v.Text()
		}
	case llm.Part:
		toolParts = []llm.Part{v}
		ev.Output = v.Text
	case []llm.Part:
		toolParts = v
		ev.Output = extractTextFromParts(v)
	case string:
		toolParts = []llm.Part{llm.TextPart(v)}
		ev.Output = v
	case []byte:
		toolParts = []llm.Part{llm.TextPart(string(v))}
		ev.Output = string(v)
	default:
		b, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			s := fmt.Sprintf("%v", v)
			toolParts = []llm.Part{llm.TextPart(s)}
			ev.Output = s
		} else {
			toolParts = []llm.Part{llm.TextPart(string(b))}
			ev.Output = string(b)
		}
	}

	hooks.afterTool(ctx, ev)
	return ev.Output, toolParts, nil
}

// extractTextFromParts joins the text parts of a multipart result with newlines.
func extractTextFromParts(parts []llm.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}
