package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/ijaidev/gojent/tools"
	"github.com/ijaidev/gojent/tools/exec"
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
}

func TestNewExecTools(t *testing.T) {
	execTools, err := tools.NewExecTools(exec.ManagerConfig{})
	if err != nil {
		t.Fatalf("failed to create managed exec tools: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() {
		if err := execTools.Close(ctx); err != nil {
			t.Errorf("close exec tools: %v", err)
		}
	}()
	if len(execTools.All()) != 5 {
		t.Errorf("expected 5 managed exec tools, got %d", len(execTools.All()))
	}
	if execTools.Manager() == nil {
		t.Error("expected a non-nil manager")
	}
}
