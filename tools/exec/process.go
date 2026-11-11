package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessStatus is the lifecycle state of a managed process.
type ProcessStatus string

const (
	// ProcessRunning means the process has started and has not been reaped.
	ProcessRunning ProcessStatus = "running"
	// ProcessExited means the process exited with code 0.
	ProcessExited ProcessStatus = "exited"
	// ProcessFailed means the process exited with a non-zero code or could not
	// be waited on successfully.
	ProcessFailed ProcessStatus = "failed"
	// ProcessKilled means the process was terminated by a signal or by a stop
	// request.
	ProcessKilled ProcessStatus = "killed"
)

// ProcessKind describes why a process is tracked.
type ProcessKind string

const (
	// ProcessForeground is a command that has not outlived its wait window.
	ProcessForeground ProcessKind = "foreground"
	// ProcessBackground is a command started with background=true.
	ProcessBackground ProcessKind = "background"
	// ProcessPromoted is a foreground command that outlived its wait window.
	ProcessPromoted ProcessKind = "promoted"
)

// Signal is the portable subset of signals accepted by Kill.
type Signal string

const (
	// SignalTerm requests a graceful stop.
	SignalTerm Signal = "TERM"
	// SignalInt requests an interrupt.
	SignalInt Signal = "INT"
	// SignalKill requests an immediate stop.
	SignalKill Signal = "KILL"
)

func (s Signal) valid() bool {
	switch s {
	case SignalTerm, SignalInt, SignalKill:
		return true
	default:
		return false
	}
}

var (
	// ErrProcessNotFound is returned when a process ID is not tracked by the
	// manager.
	ErrProcessNotFound = errors.New("exec: process not found")
	// ErrProcessLimit is returned when the manager has no room for another
	// running process.
	ErrProcessLimit = errors.New("exec: process limit reached")
	// ErrManagerClosed is returned after Manager.Close has been called.
	ErrManagerClosed = errors.New("exec: manager closed")
	// ErrCommandRequired is returned when a start request has no command.
	ErrCommandRequired = errors.New("exec: command is required")
	// ErrWorkingDirOutsideBase is returned when a requested working directory
	// escapes the configured base directory.
	ErrWorkingDirOutsideBase = errors.New("exec: working directory is outside the base directory")
	// ErrInvalidSignal is returned for an unsupported signal name.
	ErrInvalidSignal = errors.New("exec: invalid signal")
)

// Snapshot is a point-in-time view of a managed process. Tool results embed
// snapshots; they are observations, not live state.
type Snapshot struct {
	ProcessID    string        `json:"process_id"`
	PID          int           `json:"pid,omitempty"`
	Command      string        `json:"command,omitempty"`
	WorkingDir   string        `json:"working_dir,omitempty"`
	Kind         ProcessKind   `json:"kind"`
	Status       ProcessStatus `json:"status"`
	StartedAt    time.Time     `json:"started_at"`
	ObservedAt   time.Time     `json:"observed_at"`
	DurationMS   int64         `json:"duration_ms"`
	ExitCode     *int          `json:"exit_code,omitempty"`
	Signal       string        `json:"signal,omitempty"`
	StoppedBy    string        `json:"stopped_by,omitempty"`
	StdoutBytes  int64         `json:"stdout_bytes,omitempty"`
	StderrBytes  int64         `json:"stderr_bytes,omitempty"`
	OmittedBytes int64         `json:"omitted_bytes,omitempty"`
	LastOutputAt *time.Time    `json:"last_output_at,omitempty"`
	NextCursor   int64         `json:"next_cursor,omitempty"`
	Tail         string        `json:"tail,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// Chunk is a bounded read from a process's combined output.
type Chunk struct {
	ProcessID    string        `json:"process_id"`
	Output       string        `json:"output"`
	NextCursor   int64         `json:"next_cursor"`
	HasMore      bool          `json:"has_more,omitempty"`
	Truncated    bool          `json:"truncated,omitempty"`
	OmittedBytes int64         `json:"omitted_bytes,omitempty"`
	CursorReset  bool          `json:"cursor_reset,omitempty"`
	Status       ProcessStatus `json:"status"`
	ExitCode     *int          `json:"exit_code,omitempty"`
	Signal       string        `json:"signal,omitempty"`
	ObservedAt   time.Time     `json:"observed_at"`
	Note         string        `json:"note,omitempty"`
}

// StreamOutput is a bounded snapshot of stdout and stderr separately. It is
// used by the execute result, while later reads use the combined Chunk.
type StreamOutput struct {
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	StdoutOmitted   int64
	StderrOmitted   int64
	NextCursor      int64
}

type process struct {
	id       string
	kind     ProcessKind
	cmd      *osExec.Cmd
	command  string
	workDir  string
	started  time.Time
	stdout   *boundedBuffer
	stderr   *boundedBuffer
	combined *boundedBuffer
	lastOut  atomic.Int64
	timer    *time.Timer
	done     chan struct{}

	mu         sync.Mutex
	status     ProcessStatus
	exitCode   *int
	signal     string
	stoppedBy  string
	finishedAt time.Time
	waitErr    string
}

func newProcess(id string, kind ProcessKind, command, workDir string, streamBytes, bufferBytes int) *process {
	return &process{
		id:       id,
		kind:     kind,
		command:  command,
		workDir:  workDir,
		started:  time.Now(),
		stdout:   newBoundedBuffer(streamBytes),
		stderr:   newBoundedBuffer(streamBytes),
		combined: newBoundedBuffer(bufferBytes),
		status:   ProcessRunning,
		done:     make(chan struct{}),
	}
}

func (p *process) attach(cmd *osExec.Cmd) {
	p.cmd = cmd
	cmd.Stdout = &processWriter{process: p, stream: p.stdout}
	cmd.Stderr = &processWriter{process: p, stream: p.stderr}
}

// reap waits for the command and publishes its final state. It is the only
// goroutine allowed to call Wait.
func (p *process) reap() {
	err := p.cmd.Wait()
	ps := p.cmd.ProcessState

	p.mu.Lock()
	p.finishedAt = time.Now()
	stoppedBy := p.stoppedBy
	timer := p.timer
	status := ProcessFailed
	var code *int
	var signal string
	var waitErr string

	switch {
	case ps != nil:
		exitCode, signalName, signaled := processExitState(ps)
		code = intPtr(exitCode)
		signal = signalName
		switch {
		case signaled:
			status = ProcessKilled
			if stoppedBy == "" {
				p.stoppedBy = "external"
			}
		case stoppedBy != "":
			status = ProcessKilled
		case exitCode == 0:
			status = ProcessExited
		default:
			status = ProcessFailed
		}
	case err != nil:
		waitErr = err.Error()
	default:
		status = ProcessExited
		code = intPtr(0)
	}
	if err != nil && status == ProcessFailed && waitErr == "" {
		waitErr = err.Error()
	}

	p.status = status
	p.exitCode = code
	p.signal = signal
	p.waitErr = waitErr
	p.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	p.stdout.Close()
	p.stderr.Close()
	p.combined.Close()
	close(p.done)
}

func (p *process) requestStop(reason string) {
	select {
	case <-p.done:
		return
	default:
	}
	p.mu.Lock()
	if p.stoppedBy == "" {
		p.stoppedBy = reason
	}
	p.mu.Unlock()
}

func (p *process) isDone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *process) sendSignal(sig Signal) error {
	if p.isDone() {
		return os.ErrProcessDone
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := signalProcessGroup(p.cmd, sig)
	if errors.Is(err, os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	return err
}

func (p *process) snapshot(now time.Time) Snapshot {
	p.mu.Lock()
	status := p.status
	kind := p.kind
	code := cloneIntPtr(p.exitCode)
	sig := p.signal
	stoppedBy := p.stoppedBy
	finished := p.finishedAt
	waitErr := p.waitErr
	p.mu.Unlock()

	duration := now.Sub(p.started)
	if !finished.IsZero() {
		duration = finished.Sub(p.started)
	}
	if duration < 0 {
		duration = 0
	}

	var lastOut *time.Time
	if nanos := p.lastOut.Load(); nanos > 0 {
		t := time.Unix(0, nanos).UTC()
		lastOut = &t
	}

	cmdPID := 0
	if p.cmd != nil && p.cmd.Process != nil {
		cmdPID = p.cmd.Process.Pid
	}

	return Snapshot{
		ProcessID:    p.id,
		PID:          cmdPID,
		Command:      shorten(p.command, 256),
		WorkingDir:   shorten(p.workDir, 256),
		Kind:         kind,
		Status:       status,
		StartedAt:    p.started.UTC(),
		ObservedAt:   now.UTC(),
		DurationMS:   duration.Milliseconds(),
		ExitCode:     code,
		Signal:       sig,
		StoppedBy:    stoppedBy,
		StdoutBytes:  p.stdout.Total(),
		StderrBytes:  p.stderr.Total(),
		OmittedBytes: p.combined.Omitted(),
		LastOutputAt: lastOut,
		NextCursor:   p.combined.Total(),
		Tail:         p.combined.TailLines(20, 2048).String(),
		Error:        waitErr,
	}
}

func (p *process) streams(maxBytes int) StreamOutput {
	stdout := p.stdout.Snapshot(maxBytes)
	stderr := p.stderr.Snapshot(maxBytes)
	return StreamOutput{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
		StdoutOmitted:   stdout.omitted,
		StderrOmitted:   stderr.omitted,
		NextCursor:      p.combined.Total(),
	}
}

func (p *process) read(ctx context.Context, cursor int64, maxBytes int, wait time.Duration) Chunk {
	chunk := p.combined.Read(cursor, maxBytes)
	if len(chunk.data) == 0 && wait > 0 {
		p.combined.WaitForData(ctx.Done(), cursor, wait)
		chunk = p.combined.Read(cursor, maxBytes)
	}
	snap := p.snapshot(time.Now())
	return Chunk{
		ProcessID:    p.id,
		Output:       string(chunk.data),
		NextCursor:   chunk.next,
		HasMore:      chunk.hasMore,
		Truncated:    chunk.reset || chunk.omitted > 0,
		OmittedBytes: chunk.omitted,
		CursorReset:  chunk.reset,
		Status:       snap.Status,
		ExitCode:     snap.ExitCode,
		Signal:       snap.Signal,
		ObservedAt:   snap.ObservedAt,
	}
}

type processWriter struct {
	process *process
	stream  *boundedBuffer
}

func (w *processWriter) Write(data []byte) (int, error) {
	if _, err := w.stream.Write(data); err != nil {
		return 0, err
	}
	if _, err := w.process.combined.Write(data); err != nil {
		return 0, err
	}
	w.process.lastOut.Store(time.Now().UnixNano())
	return len(data), nil
}

func intPtr(value int) *int {
	return &value
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func parseSignal(raw string) (Signal, error) {
	if raw == "" {
		return SignalTerm, nil
	}
	sig := Signal(strings.ToUpper(strings.TrimSpace(raw)))
	if !sig.valid() {
		return "", fmt.Errorf("%w: %q (use TERM, INT, or KILL)", ErrInvalidSignal, raw)
	}
	return sig, nil
}

func shorten(value string, maxRunes int) string {
	if maxRunes <= 0 || len(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}
