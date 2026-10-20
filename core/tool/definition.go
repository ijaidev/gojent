package tool

import "github.com/ijaidev/gojent/core/llm"

// ToDefinition converts a Tool to an llm.ToolDefinition.
// Nil and typed-nil tools produce an empty definition.
func ToDefinition(t Tool) llm.ToolDefinition {
	if IsNilTool(t) {
		return llm.ToolDefinition{}
	}
	return llm.ToolDefinition{
		Name:        t.ID(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}

// ToDefinitions converts a slice of Tools to a slice of llm.ToolDefinition.
// Nil and typed-nil tools are skipped.
func ToDefinitions(tools []Tool) []llm.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	defs := make([]llm.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if IsNilTool(t) {
			continue
		}
		defs = append(defs, ToDefinition(t))
	}
	if len(defs) == 0 {
		return nil
	}
	return defs
}
