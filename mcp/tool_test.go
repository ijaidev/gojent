package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/tool"
)

func TestDecodeArgumentsRejectsTrailingData(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "object", raw: `{"a":1}`},
		{name: "object with whitespace", raw: " {\"a\":1}\n\t "},
		{name: "empty", raw: ``},
		{name: "null", raw: `null`},
		{name: "second object", raw: `{"a":1} {"b":2}`, wantErr: true},
		{name: "second value", raw: `{"a":1} 2`, wantErr: true},
		{name: "trailing garbage", raw: `{"a":1} nope`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeArguments(json.RawMessage(test.raw))
			if test.wantErr {
				if !errors.Is(err, tool.ErrInvalidInput) {
					t.Fatalf("error = %v, want tool.ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
		})
	}
}
