package llm

// ResponseFormatType defines the expected output format.
type ResponseFormatType string

const (
	// ResponseFormatText produces unconstrained text.
	ResponseFormatText ResponseFormatType = "text"
	// ResponseFormatJSON produces valid JSON.
	ResponseFormatJSON ResponseFormatType = "json"
	// ResponseFormatJSONSchema produces JSON conforming to Schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat constrains structured output.
type ResponseFormat struct {
	// Type selects text, JSON, or JSON Schema output.
	Type ResponseFormatType `json:"type"`
	// Name identifies the schema for providers that require a name.
	Name string `json:"name,omitempty"`
	// Schema is a JSON Schema object.
	Schema map[string]any `json:"schema,omitempty"`
	// Strict asks the provider to enforce the schema exactly.
	Strict bool `json:"strict,omitempty"`
}

// Clone returns a deep copy.
func (rf *ResponseFormat) Clone() *ResponseFormat {
	if rf == nil {
		return nil
	}
	cp := *rf
	if rf.Schema != nil {
		cp.Schema = cloneSchema(rf.Schema)
	}
	return &cp
}
