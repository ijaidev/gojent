package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
)

func TestRecoveryError_TypedNilPanicError(t *testing.T) {
	var panicErr *PanicError
	if got := recoveryError(panicErr); !strings.Contains(got, "panic") {
		t.Fatalf("recoveryError = %q, want a panic description", got)
	}
}

func TestToolErrorPayload_TypedNilPanicError(t *testing.T) {
	var panicErr *PanicError
	payload := toolErrorPayload(panicErr)
	if !json.Valid([]byte(payload)) {
		t.Fatalf("payload is not valid JSON: %q", payload)
	}
	if !strings.Contains(payload, "panic") {
		t.Fatalf("payload = %q, want a panic description", payload)
	}
}

func TestPanicError_NilReceiver(t *testing.T) {
	var panicErr *PanicError
	if got := panicErr.Error(); got == "" {
		t.Fatal("nil PanicError should render an error string")
	}
}

func TestCanonicalResult_DropsEmptyTextParts(t *testing.T) {
	result, err := canonicalResult(tool.Call{ID: "c1", Name: "tool"}, tool.Result{
		Content: []llm.Part{llm.NewTextPart(""), llm.NewTextPart("ok")},
	}, llm.ToolStatusCompleted)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(*llm.TextPart).Text != "ok" {
		t.Errorf("content = %+v, want a single %q part", result.Content, "ok")
	}
}

func TestCanonicalResult_AllEmptyPartsBecomeEmptyObject(t *testing.T) {
	result, err := canonicalResult(tool.Call{ID: "c1", Name: "tool"}, tool.Result{
		Content: []llm.Part{llm.NewTextPart("")},
	}, llm.ToolStatusCompleted)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(*llm.TextPart).Text != "{}" {
		t.Errorf("content = %+v, want a single %q part", result.Content, "{}")
	}
}
