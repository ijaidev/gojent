package exec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/tool"
)

const (
	defaultToolWait = 30 * time.Second
	maxToolWait     = 60 * time.Second
	maxReadWait     = 30 * time.Second
	maxExecuteWait  = 300 * time.Second
	maxTailLines    = 1000
	maxKillGrace    = 30 * time.Second
)

// ExecuteInput defines the input parameters for executing a shell command.
type ExecuteInput struct {
	Command           string `json:"command" description:"The command line string to execute"`
	WorkingDir        string `json:"working_dir,omitempty" description:"Optional working directory for the command"`
	TimeoutSeconds    int    `json:"timeout_seconds,omitempty" description:"Foreground wait before the command is moved to the background (default 30, max 300)"`
	Background        bool   `json:"background,omitempty" description:"Start the command in the background and return a process_id immediately"`
	MaxRuntimeSeconds int    `json:"max_runtime_seconds,omitempty" description:"Optional hard lifetime cap for a managed process (default 1800, max 7200)"`
}

// ExecuteResult is the tool result returned after a command starts, finishes,
// or is promoted to the background. It is a snapshot, not live state.
type ExecuteResult struct {
	Mode            string        `json:"mode"` // foreground or background
	Status          ProcessStatus `json:"status"`
	ProcessID       string        `json:"process_id,omitempty"`
	PID             int           `json:"pid,omitempty"`
	Stdout          string        `json:"stdout,omitempty"`
	Stderr          string        `json:"stderr,omitempty"`
	StdoutTruncated bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated bool          `json:"stderr_truncated,omitempty"`
	ExitCode        *int          `json:"exit_code,omitempty"`
	Signal          string        `json:"signal,omitempty"`
	StoppedBy       string        `json:"stopped_by,omitempty"`
	DurationMS      int64         `json:"duration_ms"`
	ObservedAt      time.Time     `json:"observed_at"`
	Promoted        bool          `json:"promoted,omitempty"`
	TimedOut        bool          `json:"timed_out,omitempty"`
	Truncated       bool          `json:"truncated,omitempty"`
	OmittedBytes    int64         `json:"omitted_bytes,omitempty"`
	NextCursor      int64         `json:"next_cursor,omitempty"`
	Note            string        `json:"note,omitempty"`
}

// ProcessStatusInput selects one process or lists tracked processes.
type ProcessStatusInput struct {
	ProcessID       string `json:"process_id,omitempty" description:"Process ID to inspect; omit to list running processes"`
	IncludeFinished bool   `json:"include_finished,omitempty" description:"Include recently finished processes in a list"`
}

// ProcessStatusOutput is either one process or a list of processes.
type ProcessStatusOutput struct {
	Process   *Snapshot  `json:"process,omitempty"`
	Processes []Snapshot `json:"processes,omitempty"`
	Total     int        `json:"total"`
}

// ReadProcessInput reads bounded output from a managed process.
type ReadProcessInput struct {
	ProcessID   string `json:"process_id" description:"Process ID returned by execute_command"`
	Cursor      *int64 `json:"cursor,omitempty" description:"Byte cursor from a previous next_cursor; returns only newer output"`
	Lines       int    `json:"lines,omitempty" description:"Number of trailing lines to return when cursor is omitted (default 100, max 1000)"`
	MaxBytes    int    `json:"max_bytes,omitempty" description:"Maximum bytes to return (default 16384, max 65536)"`
	WaitSeconds int    `json:"wait_seconds,omitempty" description:"Wait up to this many seconds for new output (max 30)"`
}

// WaitProcessInput waits for a process to exit or for a bounded timeout.
type WaitProcessInput struct {
	ProcessID      string `json:"process_id" description:"Process ID returned by execute_command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" description:"Wait budget in seconds (default 30, max 60)"`
	Cursor         *int64 `json:"cursor,omitempty" description:"Optional cursor to return only newer output"`
	MaxBytes       int    `json:"max_bytes,omitempty" description:"Maximum bytes to return (default 16384, max 65536)"`
}

// WaitProcessOutput is a process read plus whether the wait window expired.
type WaitProcessOutput struct {
	Chunk
	TimedOut   bool   `json:"timed_out,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Note       string `json:"note,omitempty"`
}

// KillProcessInput stops a tracked process.
type KillProcessInput struct {
	ProcessID    string `json:"process_id" description:"Process ID returned by execute_command"`
	Signal       string `json:"signal,omitempty" description:"TERM (default), INT, or KILL"`
	GraceSeconds int    `json:"grace_seconds,omitempty" description:"Seconds to wait after TERM before KILL (default 5, max 30)"`
}

// KillProcessOutput reports the final state of a stop request.
type KillProcessOutput struct {
	Snapshot
	Confirmed     bool   `json:"confirmed"`
	AlreadyExited bool   `json:"already_exited,omitempty"`
	Note          string `json:"note,omitempty"`
}

// Tools bundles the execute and process-management tools that share one
// manager.
type Tools struct {
	manager *Manager
	tools   []tool.Tool
}

// NewTools builds the execute and process-management tools around m. The
// caller owns m and should call Tools.Close when finished. The returned
// execute_command tool uses the managed process lifecycle; do not register it
// alongside NewExecuteCommandTool, which uses the same tool ID.
func NewTools(m *Manager) (*Tools, error) {
	if m == nil {
		return nil, errors.New("exec: manager is required")
	}
	execute, err := newExecuteTool(m)
	if err != nil {
		return nil, err
	}
	status, err := newProcessStatusTool(m)
	if err != nil {
		return nil, err
	}
	read, err := newReadProcessTool(m)
	if err != nil {
		return nil, err
	}
	wait, err := newWaitProcessTool(m)
	if err != nil {
		return nil, err
	}
	kill, err := newKillProcessTool(m)
	if err != nil {
		return nil, err
	}
	return &Tools{
		manager: m,
		tools:   []tool.Tool{execute, status, read, wait, kill},
	}, nil
}

// All returns the tool set in a stable order.
func (t *Tools) All() []tool.Tool {
	if t == nil {
		return nil
	}
	return append([]tool.Tool(nil), t.tools...)
}

// Manager returns the manager shared by the tools.
func (t *Tools) Manager() *Manager {
	if t == nil {
		return nil
	}
	return t.manager
}

// Close stops all managed processes and releases the tool set.
func (t *Tools) Close(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	return t.manager.Close(ctx)
}

func newExecuteTool(m *Manager) (tool.Tool, error) {
	return tool.New(tool.Config[ExecuteInput, ExecuteResult]{
		ID:          "execute_command",
		Description: "Execute a shell command. By default the tool waits up to timeout_seconds; if the command is still running it is moved to the background and a process_id is returned. Set background=true to start it immediately. Use process_status, read_process_output, wait_process, and kill_process to manage the process. Do not append '&'; use background instead.",
		Annotations: tool.Annotations{
			Destructive: true,
			OpenWorld:   true,
		},
		ExecutionMode: tool.ExecutionSequential,
		Execute: func(ctx context.Context, in ExecuteInput) (ExecuteResult, error) {
			if strings.TrimSpace(in.Command) == "" {
				return ExecuteResult{}, fmt.Errorf("execute_command: command cannot be empty")
			}
			if strings.HasSuffix(strings.TrimSpace(in.Command), "&") {
				return ExecuteResult{}, fmt.Errorf("execute_command: do not append '&'; use background=true")
			}
			if in.TimeoutSeconds < 0 {
				return ExecuteResult{}, fmt.Errorf("execute_command: timeout_seconds cannot be negative")
			}
			if in.MaxRuntimeSeconds < 0 {
				return ExecuteResult{}, fmt.Errorf("execute_command: max_runtime_seconds cannot be negative")
			}
			wait := m.cfg.DefaultWait
			if in.TimeoutSeconds > 0 {
				wait = durationSeconds(in.TimeoutSeconds, maxExecuteWait)
			}
			if wait > m.cfg.MaxWait {
				wait = m.cfg.MaxWait
			}
			lifetime := time.Duration(0)
			if in.MaxRuntimeSeconds > 0 {
				lifetime = durationSeconds(in.MaxRuntimeSeconds, maxProcessLifetime)
				if lifetime > m.cfg.MaxLifetime {
					lifetime = m.cfg.MaxLifetime
				}
			}
			kind := ProcessForeground
			if in.Background {
				kind = ProcessBackground
			}
			snap, err := m.Start(ctx, Spec{
				Command:    in.Command,
				WorkingDir: in.WorkingDir,
				Kind:       kind,
				MaxRuntime: lifetime,
			})
			if err != nil {
				return ExecuteResult{}, fmt.Errorf("execute_command: %w", err)
			}
			if in.Background {
				return finishBackground(ctx, m, snap)
			}
			return finishForeground(ctx, m, snap, wait)
		},
	})
}

func finishForeground(ctx context.Context, m *Manager, snap Snapshot, wait time.Duration) (ExecuteResult, error) {
	finished, stillRunning, err := m.Wait(ctx, snap.ProcessID, wait)
	if err != nil {
		// The result will not reach the agent, so do not leave an invisible
		// process behind.
		_, _, _ = m.Kill(context.Background(), snap.ProcessID, SignalKill, 0)
		return ExecuteResult{}, fmt.Errorf("execute_command: wait: %w", err)
	}
	if !stillRunning {
		streams, err := m.Streams(snap.ProcessID, m.cfg.ExecuteBytes)
		if err != nil {
			return ExecuteResult{}, fmt.Errorf("execute_command: read output: %w", err)
		}
		return resultFromSnapshot(finished, streams, "foreground", false, false, ""), nil
	}
	promoted, err := m.Promote(snap.ProcessID)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("execute_command: promote: %w", err)
	}
	streams, err := m.Streams(snap.ProcessID, m.cfg.ExecuteBytes)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("execute_command: read output: %w", err)
	}
	if promoted.Status != ProcessRunning {
		return resultFromSnapshot(promoted, streams, "foreground", true, true, "Exited just after the wait window."), nil
	}
	note := fmt.Sprintf("Did not finish within %s; moved to the background. Use wait_process, read_process_output, or kill_process with process_id=%s.", wait.Round(time.Millisecond), promoted.ProcessID)
	return resultFromSnapshot(promoted, streams, "foreground", true, true, note), nil
}

func finishBackground(ctx context.Context, m *Manager, snap Snapshot) (ExecuteResult, error) {
	finished, stillRunning, err := m.Wait(ctx, snap.ProcessID, m.cfg.ReadinessDelay)
	if err != nil {
		_, _, _ = m.Kill(context.Background(), snap.ProcessID, SignalKill, 0)
		return ExecuteResult{}, fmt.Errorf("execute_command: wait: %w", err)
	}
	streams, err := m.Streams(snap.ProcessID, m.cfg.ExecuteBytes)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("execute_command: read output: %w", err)
	}
	if stillRunning {
		note := fmt.Sprintf("Started in the background. Use process_status, read_process_output, wait_process, or kill_process with process_id=%s.", finished.ProcessID)
		return resultFromSnapshot(finished, streams, "background", false, false, note), nil
	}
	note := "Exited before the call returned."
	if finished.Status == ProcessFailed || finished.Status == ProcessKilled {
		note = "Exited before the call returned; inspect the output and exit status."
	}
	return resultFromSnapshot(finished, streams, "background", false, false, note), nil
}

func resultFromSnapshot(snap Snapshot, streams StreamOutput, mode string, promoted, timedOut bool, note string) ExecuteResult {
	includeID := snap.Status == ProcessRunning || streams.StdoutTruncated || streams.StderrTruncated
	result := ExecuteResult{
		Mode:            mode,
		Status:          snap.Status,
		Stdout:          streams.Stdout,
		Stderr:          streams.Stderr,
		StdoutTruncated: streams.StdoutTruncated,
		StderrTruncated: streams.StderrTruncated,
		ExitCode:        snap.ExitCode,
		Signal:          snap.Signal,
		StoppedBy:       snap.StoppedBy,
		DurationMS:      snap.DurationMS,
		ObservedAt:      snap.ObservedAt,
		Promoted:        promoted,
		TimedOut:        timedOut,
		Truncated:       streams.StdoutTruncated || streams.StderrTruncated,
		OmittedBytes:    streams.StdoutOmitted + streams.StderrOmitted,
		Note:            note,
	}
	if includeID {
		result.ProcessID = snap.ProcessID
		result.PID = snap.PID
		result.NextCursor = streams.NextCursor
	}
	return result
}

func newProcessStatusTool(m *Manager) (tool.Tool, error) {
	return tool.New(tool.Config[ProcessStatusInput, ProcessStatusOutput]{
		ID:          "process_status",
		Description: "Inspect a managed process by process_id, or list running processes when process_id is omitted. Status is a snapshot at observed_at; call again for current state. Set include_finished=true to include recently finished processes in a list.",
		Annotations: tool.Annotations{
			ReadOnly:   true,
			Idempotent: true,
		},
		Execute: func(_ context.Context, in ProcessStatusInput) (ProcessStatusOutput, error) {
			id := strings.TrimSpace(in.ProcessID)
			if id != "" {
				snap, err := m.Status(id)
				if err != nil {
					return ProcessStatusOutput{}, err
				}
				return ProcessStatusOutput{Process: &snap, Total: 1}, nil
			}
			snapshots := m.List()
			result := make([]Snapshot, 0, len(snapshots))
			for i := range snapshots {
				if !in.IncludeFinished && snapshots[i].Status != ProcessRunning {
					continue
				}
				result = append(result, snapshots[i])
			}
			return ProcessStatusOutput{Processes: result, Total: len(result)}, nil
		},
	})
}

func newReadProcessTool(m *Manager) (tool.Tool, error) {
	return tool.New(tool.Config[ReadProcessInput, Chunk]{
		ID:          "read_process_output",
		Description: "Read bounded output from a managed process. Pass cursor to read only output produced after a previous next_cursor, or lines to read the last N lines. The result is capped by max_bytes; use next_cursor to continue.",
		Annotations: tool.Annotations{
			ReadOnly:   true,
			Idempotent: true,
		},
		Execute: func(ctx context.Context, in ReadProcessInput) (Chunk, error) {
			id := strings.TrimSpace(in.ProcessID)
			if id == "" {
				return Chunk{}, fmt.Errorf("read_process_output: process_id is required")
			}
			if in.Lines < 0 {
				return Chunk{}, fmt.Errorf("read_process_output: lines cannot be negative")
			}
			if in.MaxBytes < 0 {
				return Chunk{}, fmt.Errorf("read_process_output: max_bytes cannot be negative")
			}
			if in.WaitSeconds < 0 {
				return Chunk{}, fmt.Errorf("read_process_output: wait_seconds cannot be negative")
			}
			wait := durationSeconds(in.WaitSeconds, maxReadWait)
			var chunk Chunk
			var err error
			if in.Cursor != nil {
				chunk, err = m.Read(ctx, id, *in.Cursor, in.MaxBytes, wait)
			} else {
				lines := in.Lines
				if lines == 0 {
					lines = defaultProcessTailLines
				}
				if lines > maxTailLines {
					lines = maxTailLines
				}
				chunk, err = m.Tail(ctx, id, lines, in.MaxBytes, wait)
			}
			if err != nil {
				return Chunk{}, err
			}
			switch {
			case chunk.CursorReset:
				chunk.Note = "Older output was dropped; the cursor was reset to the earliest retained byte."
			case chunk.Output == "" && chunk.Status == ProcessRunning:
				chunk.Note = "No new output."
			case chunk.Output == "" && chunk.Status != ProcessRunning:
				chunk.Note = "Process exited with no output."
			case chunk.Truncated:
				chunk.Note = "Output was truncated; use next_cursor to continue."
			}
			return chunk, nil
		},
	})
}

func newWaitProcessTool(m *Manager) (tool.Tool, error) {
	return tool.New(tool.Config[WaitProcessInput, WaitProcessOutput]{
		ID:          "wait_process",
		Description: "Wait up to timeout_seconds for a managed process to exit. A timeout is not an error: it returns status=running and timed_out=true. Pass cursor to return only output newer than a previous read.",
		Annotations: tool.Annotations{
			ReadOnly:   true,
			Idempotent: true,
		},
		Execute: func(ctx context.Context, in WaitProcessInput) (WaitProcessOutput, error) {
			id := strings.TrimSpace(in.ProcessID)
			if id == "" {
				return WaitProcessOutput{}, fmt.Errorf("wait_process: process_id is required")
			}
			if in.TimeoutSeconds < 0 {
				return WaitProcessOutput{}, fmt.Errorf("wait_process: timeout_seconds cannot be negative")
			}
			if in.MaxBytes < 0 {
				return WaitProcessOutput{}, fmt.Errorf("wait_process: max_bytes cannot be negative")
			}
			wait := defaultToolWait
			if in.TimeoutSeconds > 0 {
				wait = durationSeconds(in.TimeoutSeconds, maxToolWait)
			}
			snap, stillRunning, err := m.Wait(ctx, id, wait)
			if err != nil {
				return WaitProcessOutput{}, fmt.Errorf("wait_process: %w", err)
			}
			var chunk Chunk
			if in.Cursor != nil {
				chunk, err = m.Read(ctx, id, *in.Cursor, in.MaxBytes, 0)
			} else {
				chunk, err = m.Tail(ctx, id, defaultProcessTailLines, in.MaxBytes, 0)
			}
			if err != nil {
				return WaitProcessOutput{}, fmt.Errorf("wait_process: read output: %w", err)
			}
			if stillRunning && chunk.Status != ProcessRunning {
				stillRunning = false
			}
			result := WaitProcessOutput{
				Chunk:      chunk,
				TimedOut:   stillRunning,
				DurationMS: snap.DurationMS,
			}
			switch {
			case stillRunning:
				result.Note = fmt.Sprintf("Still running after %s. Call wait_process again, read more output, or do other work.", wait.Round(time.Millisecond))
			case chunk.Output == "":
				result.Note = "Process exited with no output."
			}
			return result, nil
		},
	})
}

func newKillProcessTool(m *Manager) (tool.Tool, error) {
	return tool.New(tool.Config[KillProcessInput, KillProcessOutput]{
		ID:          "kill_process",
		Description: "Stop a tracked process by process_id. Sends TERM by default, waits grace_seconds, then KILL. Only processes started by this runtime can be stopped; raw PIDs are not accepted. The result reports whether the exit was confirmed.",
		Annotations: tool.Annotations{
			Destructive: true,
			Idempotent:  true,
		},
		ExecutionMode: tool.ExecutionSequential,
		Execute: func(ctx context.Context, in KillProcessInput) (KillProcessOutput, error) {
			id := strings.TrimSpace(in.ProcessID)
			if id == "" {
				return KillProcessOutput{}, fmt.Errorf("kill_process: process_id is required")
			}
			sig, err := parseSignal(in.Signal)
			if err != nil {
				return KillProcessOutput{}, err
			}
			if in.GraceSeconds < 0 {
				return KillProcessOutput{}, fmt.Errorf("kill_process: grace_seconds cannot be negative")
			}
			grace := m.cfg.KillGrace
			if in.GraceSeconds > 0 {
				grace = durationSeconds(in.GraceSeconds, maxKillGrace)
			}
			before, err := m.Status(id)
			if err != nil {
				return KillProcessOutput{}, err
			}
			if before.Status != ProcessRunning {
				return KillProcessOutput{
					Snapshot:      before,
					Confirmed:     true,
					AlreadyExited: true,
					Note:          "Process had already exited.",
				}, nil
			}
			snap, confirmed, err := m.Kill(ctx, id, sig, grace)
			if err != nil {
				return KillProcessOutput{}, fmt.Errorf("kill_process: %w", err)
			}
			result := KillProcessOutput{Snapshot: snap, Confirmed: confirmed}
			if !confirmed {
				result.Note = "Stop was requested but the process exit could not be confirmed. Check process_status before starting a replacement."
			}
			return result, nil
		},
	})
}

func durationSeconds(value int, limit time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	if limit > 0 && value > int(limit/time.Second) {
		return limit
	}
	return time.Duration(value) * time.Second
}
