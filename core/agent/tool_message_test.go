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

func TestResultMessage_DropsEmptyTextParts(t *testing.T) {
	message, err := resultMessage(tool.Call{ID: "c1", Name: "tool"}, tool.Result{
		Content: []llm.Part{llm.TextPart(""), llm.TextPart("ok")},
	})
	if err != nil {
		t.Fatalf("resultMessage: %v", err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "ok" {
		t.Errorf("content = %+v, want a single %q part", message.Content, "ok")
	}
}

func TestResultMessage_AllEmptyPartsBecomeEmptyObject(t *testing.T) {
	message, err := resultMessage(tool.Call{ID: "c1", Name: "tool"}, tool.Result{
		Content: []llm.Part{llm.TextPart("")},
	})
	if err != nil {
		t.Fatalf("resultMessage: %v", err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "{}" {
		t.Errorf("content = %+v, want a single %q part", message.Content, "{}")
	}
}
