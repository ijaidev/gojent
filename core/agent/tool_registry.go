package agent

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

// toolRegistry indexes the agent and per-run tools by name. Later entries
// with the same name replace earlier ones.
type toolRegistry struct {
	ordered []tool.Tool
	byName  map[string]tool.Tool
}

// validateTools rejects tools that cannot be registered by name. Names must be
// unique within a list; replacing an agent tool with a run tool is handled
// separately by newToolRegistry.
func validateTools(tools []tool.Tool) error {
	seen := make(map[string]struct{}, len(tools))
	for _, candidate := range tools {
		if tool.IsNilTool(candidate) {
			return ErrToolNil
		}
		name := candidate.Spec().Name
		if name == "" {
			return ErrToolNameRequired
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateTool, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func newToolRegistry(agentTools, extra []tool.Tool) *toolRegistry {
	merged := make(map[string]tool.Tool, len(agentTools)+len(extra))
	for _, candidate := range agentTools {
		if !tool.IsNilTool(candidate) {
			merged[candidate.Spec().Name] = candidate
		}
	}
	for _, candidate := range extra {
		if !tool.IsNilTool(candidate) {
			merged[candidate.Spec().Name] = candidate
		}
	}
	ordered := make([]tool.Tool, 0, len(merged))
	for _, candidate := range merged {
		ordered = append(ordered, candidate)
	}
	slices.SortFunc(ordered, func(a, b tool.Tool) int {
		return cmp.Compare(a.Spec().Name, b.Spec().Name)
	})
	return &toolRegistry{ordered: ordered, byName: merged}
}

func (r *toolRegistry) definitions() []llm.ToolDefinition {
	if r == nil {
		return nil
	}
	return tool.Definitions(r.ordered)
}
