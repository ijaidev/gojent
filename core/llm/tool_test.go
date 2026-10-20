package llm_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestToolCall_UnmarshalArgs(t *testing.T) {
	tc := llm.ToolCall{
		ID:        "call-abc",
		Name:      "calc",
		Arguments: `{"a": 10, "b": 20}`,
	}

	var parsed struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	err := tc.UnmarshalArgs(&parsed)
	if err != nil {
		t.Fatalf("UnmarshalArgs failed: %v", err)
	}
	if parsed.A != 10 || parsed.B != 20 {
		t.Errorf("unexpected parsed values: %+v", parsed)
	}
}
