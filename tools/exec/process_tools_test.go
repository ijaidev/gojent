package exec_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/tools/exec"
)

func newTestTools(t *testing.T, cfg exec.ManagerConfig) *exec.Tools {
	t.Helper()
	if cfg.BaseDir == "" {
		cfg.BaseDir = t.TempDir()
	}
	m, err := exec.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tools, err := exec.NewTools(m)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tools.Close(ctx)
	})
	return tools
}

func toolByName(t *testing.T, tools *exec.Tools, name string) tool.Tool {
	t.Helper()
	for _, candidate := range tools.All() {
		if candidate.Spec().Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func callTool[T any](t *testing.T, t0 tool.Tool, input any) T {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := t0.Execute(context.Background(), tool.Call{Arguments: encoded})
	if err != nil {
		t.Fatalf("%s: %v", t0.Spec().Name, err)
	}
	typed, ok := result.Data.(T)
	if !ok {
		t.Fatalf("%s returned %T, want %T", t0.Spec().Name, result.Data, typed)
	}
	return typed
}

func TestTools_All(t *testing.T) {
	tools := newTestTools(t, exec.ManagerConfig{})
	names := make([]string, 0, len(tools.All()))
	for _, candidate := range tools.All() {
		names = append(names, candidate.Spec().Name)
	}
	want := []string{"execute_command", "process_status", "read_process_output", "wait_process", "kill_process"}
	if len(names) != len(want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools = %v, want %v", names, want)
		}
	}
}

func TestExecuteTool_Foreground(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	result := callTool[exec.ExecuteResult](t, toolByName(t, tools, "execute_command"), exec.ExecuteInput{
		Command:        "printf hello",
		TimeoutSeconds: 5,
	})
	if result.Status != exec.ProcessExited {
		t.Fatalf("status = %q, want exited", result.Status)
	}
	if result.Mode != "foreground" {
		t.Fatalf("mode = %q, want foreground", result.Mode)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Fatalf("stdout = %q, want hello", result.Stdout)
	}
	if result.ProcessID != "" {
		t.Fatalf("process_id = %q, want empty for a fully captured result", result.ProcessID)
	}
}

func TestExecuteTool_BackgroundAndRead(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	status := toolByName(t, tools, "process_status")
	read := toolByName(t, tools, "read_process_output")
	kill := toolByName(t, tools, "kill_process")

	result := callTool[exec.ExecuteResult](t, execute, exec.ExecuteInput{
		Command:    "printf ready; sleep 10",
		Background: true,
	})
	defer callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{ProcessID: result.ProcessID})

	if result.Status != exec.ProcessRunning {
		t.Fatalf("status = %q, want running", result.Status)
	}
	if result.Mode != "background" {
		t.Fatalf("mode = %q, want background", result.Mode)
	}
	if result.ProcessID == "" {
		t.Fatal("process_id is empty")
	}
	if !strings.Contains(result.Note, result.ProcessID) {
		t.Fatalf("note = %q, want process id", result.Note)
	}

	statusResult := callTool[exec.ProcessStatusOutput](t, status, exec.ProcessStatusInput{ProcessID: result.ProcessID})
	if statusResult.Process == nil || statusResult.Process.Status != exec.ProcessRunning {
		t.Fatalf("status result = %+v, want running", statusResult)
	}

	output := callTool[exec.Chunk](t, read, exec.ReadProcessInput{
		ProcessID:   result.ProcessID,
		Lines:       20,
		WaitSeconds: 2,
	})
	if !strings.Contains(output.Output, "ready") {
		t.Fatalf("output = %q, want ready", output.Output)
	}
}

func TestExecuteTool_Promotion(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	kill := toolByName(t, tools, "kill_process")

	result := callTool[exec.ExecuteResult](t, execute, exec.ExecuteInput{
		Command:        "printf start; sleep 10",
		TimeoutSeconds: 1,
	})
	defer callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{ProcessID: result.ProcessID})

	if result.Status != exec.ProcessRunning {
		t.Fatalf("status = %q, want running", result.Status)
	}
	if !result.Promoted || !result.TimedOut {
		t.Fatalf("promoted = %v, timed_out = %v, want both true", result.Promoted, result.TimedOut)
	}
	if result.ProcessID == "" {
		t.Fatal("process_id is empty after promotion")
	}
	if !strings.Contains(result.Note, "moved to the background") {
		t.Fatalf("note = %q", result.Note)
	}
}

func TestExecuteTool_BackgroundExitsImmediately(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{ReadinessDelay: 2 * time.Second})
	result := callTool[exec.ExecuteResult](t, toolByName(t, tools, "execute_command"), exec.ExecuteInput{
		Command:    "printf done",
		Background: true,
	})
	if result.Status != exec.ProcessExited {
		t.Fatalf("status = %q, want exited", result.Status)
	}
	if result.Mode != "background" {
		t.Fatalf("mode = %q, want background", result.Mode)
	}
	if !strings.Contains(result.Stdout, "done") {
		t.Fatalf("stdout = %q, want done", result.Stdout)
	}
	if !strings.Contains(result.Note, "Exited before the call returned") {
		t.Fatalf("note = %q", result.Note)
	}
}

func TestWaitTool_Timeout(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	wait := toolByName(t, tools, "wait_process")
	kill := toolByName(t, tools, "kill_process")

	started := callTool[exec.ExecuteResult](t, execute, exec.ExecuteInput{
		Command:    "sleep 10",
		Background: true,
	})
	defer callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{ProcessID: started.ProcessID})

	result := callTool[exec.WaitProcessOutput](t, wait, exec.WaitProcessInput{
		ProcessID:      started.ProcessID,
		TimeoutSeconds: 1,
	})
	if !result.TimedOut {
		t.Fatal("expected timed_out")
	}
	if result.Status != exec.ProcessRunning {
		t.Fatalf("status = %q, want running", result.Status)
	}
	if !strings.Contains(result.Note, "Still running") {
		t.Fatalf("note = %q", result.Note)
	}
}

func TestKillTool_Idempotent(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	kill := toolByName(t, tools, "kill_process")

	started := callTool[exec.ExecuteResult](t, execute, exec.ExecuteInput{
		Command:    "sleep 10",
		Background: true,
	})
	first := callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{
		ProcessID:    started.ProcessID,
		GraceSeconds: 1,
	})
	if !first.Confirmed {
		t.Fatal("first kill was not confirmed")
	}
	if first.Status != exec.ProcessKilled {
		t.Fatalf("status = %q, want killed", first.Status)
	}
	second := callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{ProcessID: started.ProcessID})
	if !second.AlreadyExited {
		t.Fatal("second kill did not report an exited process")
	}
	if !second.Confirmed {
		t.Fatal("second kill should be confirmed")
	}
}

func TestProcessStatusTool_List(t *testing.T) {
	requirePOSIXShell(t)
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	status := toolByName(t, tools, "process_status")
	kill := toolByName(t, tools, "kill_process")

	started := callTool[exec.ExecuteResult](t, execute, exec.ExecuteInput{
		Command:    "sleep 10",
		Background: true,
	})
	defer callTool[exec.KillProcessOutput](t, kill, exec.KillProcessInput{ProcessID: started.ProcessID})

	result := callTool[exec.ProcessStatusOutput](t, status, exec.ProcessStatusInput{})
	if result.Total == 0 {
		t.Fatal("expected at least one process")
	}
	found := false
	for _, candidate := range result.Processes {
		if candidate.ProcessID == started.ProcessID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("process %s not in list %+v", started.ProcessID, result.Processes)
	}
}

func TestTools_UnknownProcess(t *testing.T) {
	tools := newTestTools(t, exec.ManagerConfig{})
	read := toolByName(t, tools, "read_process_output")
	encoded, _ := json.Marshal(exec.ReadProcessInput{ProcessID: "proc_missing"})
	_, err := read.Execute(context.Background(), tool.Call{Arguments: encoded})
	if !errors.Is(err, exec.ErrProcessNotFound) {
		t.Fatalf("error = %v, want ErrProcessNotFound", err)
	}
}

func TestWaitProcessOutput_JSON(t *testing.T) {
	encoded, err := json.Marshal(exec.WaitProcessOutput{
		Chunk:    exec.Chunk{ProcessID: "proc_1"},
		TimedOut: true,
		Note:     "still running",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Count(string(encoded), `"note"`) != 1 {
		t.Fatalf("JSON has duplicate note fields: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"timed_out":true`) {
		t.Fatalf("JSON missing timed_out: %s", encoded)
	}
}

func TestExecuteTool_RejectsOutsideWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX path expectations")
	}
	base := t.TempDir()
	outside := t.TempDir()
	tools := newTestTools(t, exec.ManagerConfig{BaseDir: base})
	execute := toolByName(t, tools, "execute_command")
	encoded, err := json.Marshal(exec.ExecuteInput{
		Command:    "printf nope",
		WorkingDir: outside,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = execute.Execute(context.Background(), tool.Call{Arguments: encoded})
	if !errors.Is(err, exec.ErrWorkingDirOutsideBase) {
		t.Fatalf("error = %v, want ErrWorkingDirOutsideBase", err)
	}
}

func TestExecuteTool_RejectsTrailingAmpersand(t *testing.T) {
	tools := newTestTools(t, exec.ManagerConfig{})
	execute := toolByName(t, tools, "execute_command")
	encoded, err := json.Marshal(exec.ExecuteInput{Command: "printf nope &"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = execute.Execute(context.Background(), tool.Call{Arguments: encoded})
	if err == nil || !strings.Contains(err.Error(), "background=true") {
		t.Fatalf("error = %v, want a background=true hint", err)
	}
}
