package tool_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/tool"
)

func TestValidate_TypeKeywords(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		input   string
		wantErr bool
	}{
		{
			name:   "number accepts a number",
			schema: map[string]any{"type": "number"},
			input:  `42`,
		},
		{
			name:    "number rejects a string",
			schema:  map[string]any{"type": "number"},
			input:   `"42"`,
			wantErr: true,
		},
		{
			name:    "integer rejects a string",
			schema:  map[string]any{"type": "integer"},
			input:   `"42"`,
			wantErr: true,
		},
		{
			name:   "integer accepts an integral float",
			schema: map[string]any{"type": "integer"},
			input:  `42.0`,
		},
		{
			name:    "integer rejects a fraction",
			schema:  map[string]any{"type": "integer"},
			input:   `42.5`,
			wantErr: true,
		},
		{
			name:    "minimum rejects a string",
			schema:  map[string]any{"type": "number", "minimum": 5},
			input:   `"42"`,
			wantErr: true,
		},
		{
			name:   "minimum evaluates a number",
			schema: map[string]any{"type": "number", "minimum": 5},
			input:  `42`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tool.Validate(tc.schema, []byte(tc.input))
			if tc.wantErr {
				if !errors.Is(err, tool.ErrInvalidInput) {
					t.Fatalf("Validate(%s) = %v, want ErrInvalidInput", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%s) = %v", tc.input, err)
			}
		})
	}
}

func TestValidate_RejectsTrailingData(t *testing.T) {
	err := tool.Validate(map[string]any{"type": "object"}, []byte(`{"a":1} garbage`))
	if !errors.Is(err, tool.ErrInvalidInput) {
		t.Fatalf("Validate = %v, want ErrInvalidInput", err)
	}
}

func TestValidate_ComparesNumbersNumerically(t *testing.T) {
	schema := map[string]any{"enum": []any{json.Number("1.0"), json.Number("2")}}
	for _, input := range []string{`1`, `1.0`, `2`} {
		if err := tool.Validate(schema, []byte(input)); err != nil {
			t.Errorf("Validate(%s) = %v", input, err)
		}
	}
	if err := tool.Validate(schema, []byte(`3`)); !errors.Is(err, tool.ErrInvalidInput) {
		t.Errorf("Validate(3) = %v, want ErrInvalidInput", err)
	}
}
