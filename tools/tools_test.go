package tools_test

import (
	"testing"

	"github.com/ijaidev/gojent/tools"
)

func TestStandardToolsBundles(t *testing.T) {
	fsTools, err := tools.StandardFilesystemTools()
	if err != nil {
		t.Fatalf("failed to create fs tools: %v", err)
	}
	if len(fsTools) != 4 {
		t.Errorf("expected 4 fs tools, got %d", len(fsTools))
	}

	webTools, err := tools.StandardWebTools()
	if err != nil {
		t.Fatalf("failed to create web tools: %v", err)
	}
	if len(webTools) != 1 {
		t.Errorf("expected 1 web tool, got %d", len(webTools))
	}

	execTools, err := tools.StandardExecTools()
	if err != nil {
		t.Fatalf("failed to create exec tools: %v", err)
	}
	if len(execTools) != 1 {
		t.Errorf("expected 1 exec tool, got %d", len(execTools))
	}

	tracker, taskTools, err := tools.StandardTaskTools()
	if err != nil {
		t.Fatalf("failed to create task tools: %v", err)
	}
	if tracker == nil || len(taskTools) != 3 {
		t.Errorf("expected 3 task tools, got %d", len(taskTools))
	}

	hitlTools, err := tools.StandardHITLTools()
	if err != nil {
		t.Fatalf("failed to create hitl tools: %v", err)
	}
	if len(hitlTools) != 1 {
		t.Errorf("expected 1 hitl tool, got %d", len(hitlTools))
	}
}
