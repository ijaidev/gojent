package agent

import (
	"context"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// SubagentInput is the input schema for an agent exposed as a tool.
type SubagentInput struct {
	Prompt string `json:"prompt" description:"The task or question to delegate to the subagent"`
}

// AsToolConfig configures an agent tool.
type AsToolConfig struct {
	// Name overrides the tool name. Defaults to the sanitized agent name.
	Name string
	// Description overrides the tool description. Defaults to the agent
	// description.
	Description string
	// Options configures the subagent run. Defaults to zero-value options.
	Options RunOptions
}

// AsTool exposes the agent as a tool so another agent can delegate to it.
func (a *Agent) AsTool(cfg ...AsToolConfig) tool.Tool {
	var config AsToolConfig
	if len(cfg) > 0 {
		config = cfg[0]
	}
	name := config.Name
	if name == "" {
		name = sanitizeToolName(a.Name())
	}
	description := config.Description
	if description == "" {
		description = a.Description()
	}
	if description == "" {
		description = "Delegate a task to the " + a.Name() + " agent."
	}

	return tool.MustNew(tool.Config[SubagentInput, tool.Result]{
		ID:          name,
		Description: description,
		Execute: func(ctx context.Context, input SubagentInput) (tool.Result, error) {
			r, err := newRunner(a, config.Options)
			if err != nil {
				return tool.Result{}, err
			}
			response, err := r.run(ctx, llm.UserMessage(input.Prompt))
			if err != nil {
				return tool.Result{}, err
			}
			result := tool.Result{Data: response}
			if response.Text != "" {
				result.Content = []llm.Part{llm.TextPart(response.Text)}
			}
			return result, nil
		},
	})
}

func sanitizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "agent"
	}
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		default:
			return '_'
		}
	}, trimmed)
	return cleaned
}
