package exec_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/tools/exec"
)

func TestExecuteCommand_Success(t *testing.T) {
	cmdTool, err := exec.NewExecuteCommandTool()
	if err != nil {
		t.Fatalf("failed to create execute tool: %v", err)
	}

	inputBytes, _ := json.Marshal(exec.CommandInput{
		Command: "echo 'hello from gojent'",
	})

	resAny, err := cmdTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	res := resAny.(exec.CommandOutput)
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello from gojent") {
		t.Errorf("unexpected stdout: %q", res.Stdout)
	}
}

func TestExecuteCommand_Failure(t *testing.T) {
	cmdTool, err := exec.NewExecuteCommandTool()
	if err != nil {
		t.Fatalf("failed to create execute tool: %v", err)
	}

	inputBytes, _ := json.Marshal(exec.CommandInput{
		Command: "nonexistent_command_12345",
	})

	resAny, err := cmdTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("expected non-nil result even on exit failure, got error: %v", err)
	}

	res := resAny.(exec.CommandOutput)
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for invalid command, got %d", res.ExitCode)
	}
}

func TestExecuteCommand_Timeout(t *testing.T) {
	cmdTool, err := exec.NewExecuteCommandTool(exec.Config{
		DefaultTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create execute tool: %v", err)
	}

	inputBytes, _ := json.Marshal(exec.CommandInput{
		Command:        "sleep 2",
		TimeoutSeconds: 1, // Will timeout with 1s sleep vs 2s
	})

	start := time.Now()
	resAny, err := cmdTool.Execute(context.Background(), inputBytes)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("expected timed out CommandOutput, got error: %v", err)
	}

	res := resAny.(exec.CommandOutput)
	if !res.TimedOut {
		t.Errorf("expected TimedOut to be true, got false (duration: %v)", duration)
	}
}

func TestExecuteCommand_TimeoutKillsChildProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on POSIX shell background jobs")
	}

	cmdTool, err := exec.NewExecuteCommandTool()
	if err != nil {
		t.Fatalf("failed to create execute tool: %v", err)
	}

	// The background sleep inherits the output pipes. Killing only the shell
	// leaves it running and blocks Wait until WaitDelay expires.
	inputBytes, _ := json.Marshal(exec.CommandInput{
		Command:        "sleep 10 & sleep 10",
		TimeoutSeconds: 1,
	})

	start := time.Now()
	resAny, err := cmdTool.Execute(context.Background(), inputBytes)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected timed out CommandOutput, got error: %v", err)
	}

	res := resAny.(exec.CommandOutput)
	if !res.TimedOut {
		t.Errorf("expected TimedOut to be true after %v", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("timeout took %v; child processes kept the output pipes open", elapsed)
	}
}
