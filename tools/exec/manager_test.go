package exec_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/tools/exec"
)

func newTestManager(t *testing.T, cfg exec.ManagerConfig) *exec.Manager {
	t.Helper()
	if cfg.BaseDir == "" {
		cfg.BaseDir = t.TempDir()
	}
	m, err := exec.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.Close(ctx)
	})
	return m
}

func requirePOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell commands")
	}
}

func TestManager_ForegroundExit(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{Command: "printf hello"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finished, still, err := m.Wait(ctx, snap.ProcessID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if still {
		t.Fatal("process is still running")
	}
	if finished.Status != exec.ProcessExited {
		t.Fatalf("status = %q, want exited", finished.Status)
	}
	if finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", finished.ExitCode)
	}
	streams, err := m.Streams(snap.ProcessID, 1024)
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if !strings.Contains(streams.Stdout, "hello") {
		t.Fatalf("stdout = %q, want hello", streams.Stdout)
	}
}

func TestManager_BackgroundReadAndKill(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{Command: "printf ready; sleep 10", Kind: exec.ProcessBackground})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	chunk, err := m.Tail(ctx, snap.ProcessID, 10, 1024, 2*time.Second)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if !strings.Contains(chunk.Output, "ready") {
		t.Fatalf("output = %q, want ready", chunk.Output)
	}

	killed, confirmed, err := m.Kill(ctx, snap.ProcessID, exec.SignalTerm, 2*time.Second)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !confirmed {
		t.Fatal("kill was not confirmed")
	}
	if killed.Status != exec.ProcessKilled {
		t.Fatalf("status = %q, want killed", killed.Status)
	}
	if killed.StoppedBy != "agent" {
		t.Fatalf("stopped_by = %q, want agent", killed.StoppedBy)
	}
}

func TestManager_Promotion(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{Command: "printf start; sleep 10"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, still, err := m.Wait(ctx, snap.ProcessID, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !still {
		t.Fatal("expected process to still be running")
	}
	promoted, err := m.Promote(snap.ProcessID)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Kind != exec.ProcessPromoted {
		t.Fatalf("kind = %q, want promoted", promoted.Kind)
	}
	_, _, _ = m.Kill(ctx, snap.ProcessID, exec.SignalKill, 0)
}

func TestManager_ReadCursorWaitsForNewOutput(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{
		Command: "printf one; sleep 1; printf two; sleep 10",
		Kind:    exec.ProcessBackground,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	first, err := m.Tail(ctx, snap.ProcessID, 10, 1024, 2*time.Second)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if !strings.Contains(first.Output, "one") {
		t.Fatalf("first output = %q, want one", first.Output)
	}
	next, err := m.Read(ctx, snap.ProcessID, first.NextCursor, 1024, 2*time.Second)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(next.Output, "two") {
		t.Fatalf("next output = %q, want two", next.Output)
	}
	_, _, _ = m.Kill(ctx, snap.ProcessID, exec.SignalKill, 0)
}

func TestManager_OutputTruncation(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{
		BufferBytes:  4096,
		StreamBytes:  1024,
		ExecuteBytes: 1024,
		MaxReadBytes: 1024,
	})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{Command: "yes x | head -c 500000"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finished, still, err := m.Wait(ctx, snap.ProcessID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if still {
		t.Fatal("process is still running")
	}
	if finished.Status != exec.ProcessExited {
		t.Fatalf("status = %q, want exited", finished.Status)
	}
	streams, err := m.Streams(snap.ProcessID, 1024)
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if !streams.StdoutTruncated {
		t.Fatal("expected truncated stdout")
	}
	if streams.StdoutOmitted == 0 {
		t.Fatal("expected omitted stdout bytes")
	}
}

func TestManager_ProcessLimit(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{MaxProcesses: 1})
	ctx := context.Background()

	first, err := m.Start(ctx, exec.Spec{Command: "sleep 10", Kind: exec.ProcessBackground})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if _, err := m.Start(ctx, exec.Spec{Command: "sleep 10", Kind: exec.ProcessBackground}); !errors.Is(err, exec.ErrProcessLimit) {
		t.Fatalf("second start error = %v, want ErrProcessLimit", err)
	}
	_, _, _ = m.Kill(ctx, first.ProcessID, exec.SignalKill, 0)
}

func TestManager_WorkingDirContainment(t *testing.T) {
	requirePOSIXShell(t)
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	m := newTestManager(t, exec.ManagerConfig{BaseDir: base})
	ctx := context.Background()

	if _, err := m.Start(ctx, exec.Spec{Command: "pwd", WorkingDir: outside}); !errors.Is(err, exec.ErrWorkingDirOutsideBase) {
		t.Fatalf("absolute outside error = %v, want ErrWorkingDirOutsideBase", err)
	}
	if _, err := m.Start(ctx, exec.Spec{Command: "pwd", WorkingDir: ".."}); !errors.Is(err, exec.ErrWorkingDirOutsideBase) {
		t.Fatalf("relative outside error = %v, want ErrWorkingDirOutsideBase", err)
	}
	inside, err := m.Start(ctx, exec.Spec{Command: "pwd", WorkingDir: "sub"})
	if err != nil {
		t.Fatalf("inside start: %v", err)
	}
	_, still, err := m.Wait(ctx, inside.ProcessID, 2*time.Second)
	if err != nil {
		t.Fatalf("inside wait: %v", err)
	}
	if still {
		t.Fatal("inside process is still running")
	}
}

func TestManager_Lifetime(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{
		Command:    "sleep 10",
		Kind:       exec.ProcessBackground,
		MaxRuntime: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finished, still, err := m.Wait(ctx, snap.ProcessID, 3*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if still {
		t.Fatal("process survived its lifetime cap")
	}
	if finished.Status != exec.ProcessKilled {
		t.Fatalf("status = %q, want killed", finished.Status)
	}
	if finished.StoppedBy != "timeout" {
		t.Fatalf("stopped_by = %q, want timeout", finished.StoppedBy)
	}
}

func TestManager_CloseStopsProcesses(t *testing.T) {
	requirePOSIXShell(t)
	m := newTestManager(t, exec.ManagerConfig{})
	ctx := context.Background()

	snap, err := m.Start(ctx, exec.Spec{Command: "sleep 10", Kind: exec.ProcessBackground})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	finished, err := m.Status(snap.ProcessID)
	if err != nil {
		t.Fatalf("Status after Close: %v", err)
	}
	if finished.Status != exec.ProcessKilled {
		t.Fatalf("status = %q, want killed", finished.Status)
	}
	if finished.StoppedBy != "shutdown" {
		t.Fatalf("stopped_by = %q, want shutdown", finished.StoppedBy)
	}
	if _, err := m.Start(ctx, exec.Spec{Command: "printf nope"}); !errors.Is(err, exec.ErrManagerClosed) {
		t.Fatalf("start after close error = %v, want ErrManagerClosed", err)
	}
}

func TestManager_UnknownProcess(t *testing.T) {
	m := newTestManager(t, exec.ManagerConfig{})
	if _, err := m.Status("proc_missing"); !errors.Is(err, exec.ErrProcessNotFound) {
		t.Fatalf("Status error = %v, want ErrProcessNotFound", err)
	}
}
