package llm

// cloneSchema deep-copies a JSON Schema object. Schemas are deliberately
// untyped maps, so only the container types the package produces are copied;
// scalar values are immutable.
func cloneSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch value := v.(type) {
		case map[string]any:
			out[k] = cloneSchema(value)
		case []any:
			items := make([]any, len(value))
			for i, item := range value {
				if nested, ok := item.(map[string]any); ok {
					items[i] = cloneSchema(nested)
				} else {
					items[i] = item
				}
			}
			out[k] = items
		case []string:
			out[k] = append([]string(nil), value...)
		default:
			out[k] = v
		}
	}
	return out
}
