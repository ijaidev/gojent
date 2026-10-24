package tool

import "github.com/ijaidev/gojent/core/llm"

// Definition converts a tool into the provider-neutral definition sent to a
// model. Nil and typed-nil tools produce an empty definition.
func Definition(t Tool) llm.ToolDefinition {
	if IsNilTool(t) {
		return llm.ToolDefinition{}
	}
	spec := t.Spec()
	return llm.ToolDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Parameters:  spec.InputSchema,
		Strict:      spec.Strict,
	}
}

// Definitions converts tools into model definitions, skipping nil entries.
func Definitions(tools []Tool) []llm.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	defs := make([]llm.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if IsNilTool(t) {
			continue
		}
		defs = append(defs, Definition(t))
	}
	if len(defs) == 0 {
		return nil
	}
	return defs
}
