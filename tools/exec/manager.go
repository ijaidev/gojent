package exec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultProcessBufferBytes = 256 * 1024
	defaultProcessStreamBytes = 64 * 1024
	defaultProcessReadBytes   = 16 * 1024
	defaultProcessMaxRead     = 64 * 1024
	defaultProcessExecute     = 16 * 1024
	defaultProcessTailLines   = 100
	defaultProcessMax         = 8
	defaultFinishedRecords    = 32
	defaultProcessWait        = 30 * time.Second
	defaultProcessMaxWait     = 300 * time.Second
	defaultProcessLifetime    = 30 * time.Minute
	maxProcessLifetime        = 2 * time.Hour
	defaultProcessKillGrace   = 5 * time.Second
	defaultProcessWaitDelay   = 5 * time.Second
	defaultReadinessDelay     = 250 * time.Millisecond
	defaultKillConfirm        = 2 * time.Second
)

// ManagerConfig configures a process manager. Zero values use conservative
// defaults; the manager never uses package globals.
type ManagerConfig struct {
	// BaseDir bounds working directories when set. It is not a sandbox:
	// commands still run with the host user's permissions.
	BaseDir string
	// MaxProcesses is the number of concurrently running processes.
	MaxProcesses int
	// MaxFinished is the number of finished process records retained for late
	// reads.
	MaxFinished int
	// BufferBytes bounds the combined output retained per process.
	BufferBytes int
	// StreamBytes bounds the separately retained stdout and stderr per
	// process.
	StreamBytes int
	// MaxReadBytes caps one process read.
	MaxReadBytes int
	// ExecuteBytes caps each of stdout and stderr returned by
	// execute_command.
	ExecuteBytes int
	// DefaultWait is the default foreground wait before promotion.
	DefaultWait time.Duration
	// MaxWait caps the foreground wait.
	MaxWait time.Duration
	// DefaultLifetime is the default hard lifetime for a managed process.
	DefaultLifetime time.Duration
	// MaxLifetime caps a requested lifetime.
	MaxLifetime time.Duration
	// KillGrace is the time between TERM and KILL.
	KillGrace time.Duration
	// WaitDelay bounds how long Wait blocks for inherited output pipes.
	WaitDelay time.Duration
	// ReadinessDelay is the short wait after a background start that catches
	// immediate failures.
	ReadinessDelay time.Duration
}

// Manager owns the lifetime of background and foreground processes. It is safe
// for concurrent use and must be closed by its owner.
type Manager struct {
	cfg ManagerConfig

	mu        sync.Mutex
	closed    bool
	processes map[string]*process
}

// NewManager validates cfg and returns an empty manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.BaseDir != "" {
		abs, err := filepath.Abs(cfg.BaseDir)
		if err != nil {
			return nil, fmt.Errorf("exec: resolve base directory: %w", err)
		}
		cfg.BaseDir = filepath.Clean(abs)
	}
	if cfg.MaxProcesses <= 0 {
		cfg.MaxProcesses = defaultProcessMax
	}
	if cfg.MaxFinished <= 0 {
		cfg.MaxFinished = defaultFinishedRecords
	}
	if cfg.BufferBytes <= 0 {
		cfg.BufferBytes = defaultProcessBufferBytes
	}
	if cfg.StreamBytes <= 0 {
		cfg.StreamBytes = defaultProcessStreamBytes
	}
	if cfg.MaxReadBytes <= 0 {
		cfg.MaxReadBytes = defaultProcessMaxRead
	}
	if cfg.ExecuteBytes <= 0 {
		cfg.ExecuteBytes = defaultProcessExecute
	}
	if cfg.DefaultWait <= 0 {
		cfg.DefaultWait = defaultProcessWait
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = defaultProcessMaxWait
	}
	if cfg.MaxWait < cfg.DefaultWait {
		cfg.MaxWait = cfg.DefaultWait
	}
	if cfg.DefaultLifetime <= 0 {
		cfg.DefaultLifetime = defaultProcessLifetime
	}
	if cfg.MaxLifetime <= 0 {
		cfg.MaxLifetime = maxProcessLifetime
	}
	if cfg.MaxLifetime < cfg.DefaultLifetime {
		cfg.MaxLifetime = cfg.DefaultLifetime
	}
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = defaultProcessKillGrace
	}
	if cfg.WaitDelay <= 0 {
		cfg.WaitDelay = defaultProcessWaitDelay
	}
	if cfg.ReadinessDelay == 0 {
		cfg.ReadinessDelay = defaultReadinessDelay
	}
	return &Manager{
		cfg:       cfg,
		processes: make(map[string]*process),
	}, nil
}

// Config returns a copy of the effective configuration.
func (m *Manager) Config() ManagerConfig {
	return m.cfg
}

// Start launches a process. Once started, the process is owned by the manager
// and is not tied to ctx; use Kill, its lifetime cap, or Close to stop it.
func (m *Manager) Start(ctx context.Context, spec Spec) (Snapshot, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return Snapshot{}, ErrCommandRequired
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = ProcessForeground
	}
	switch kind {
	case ProcessForeground, ProcessBackground:
	default:
		return Snapshot{}, fmt.Errorf("exec: invalid process kind %q", kind)
	}
	lifetime := spec.MaxRuntime
	if lifetime < 0 {
		return Snapshot{}, fmt.Errorf("exec: max runtime cannot be negative")
	}
	if lifetime == 0 {
		lifetime = m.cfg.DefaultLifetime
	}
	if lifetime > m.cfg.MaxLifetime {
		lifetime = m.cfg.MaxLifetime
	}
	workDir, err := resolveWorkDir(m.cfg.BaseDir, spec.WorkingDir)
	if err != nil {
		return Snapshot{}, err
	}
	id, err := newProcessID()
	if err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Snapshot{}, ErrManagerClosed
	}
	m.pruneFinishedLocked()
	running := 0
	for _, p := range m.processes {
		if !p.isDone() {
			running++
		}
	}
	if running >= m.cfg.MaxProcesses {
		m.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w: %d running", ErrProcessLimit, running)
	}
	p := newProcess(id, kind, spec.Command, workDir, m.cfg.StreamBytes, m.cfg.BufferBytes)
	m.processes[id] = p
	m.mu.Unlock()

	cmd := shellCommand(spec.Command)
	configureManagedProcessGroup(cmd)
	cmd.WaitDelay = m.cfg.WaitDelay
	if workDir != "" {
		cmd.Dir = workDir
	}
	p.attach(cmd)

	if err := cmd.Start(); err != nil {
		m.remove(id)
		p.stdout.Close()
		p.stderr.Close()
		p.combined.Close()
		return Snapshot{}, fmt.Errorf("exec: start command: %w", err)
	}

	// Reap immediately so a cancellation race or a very short-lived command
	// still has an owner that calls Wait. The lifetime timer is armed before
	// the reaper can observe it, and the reaper stops it on exit.
	if lifetime > 0 {
		timer := time.AfterFunc(lifetime, func() {
			p.requestStop("timeout")
			_, _ = m.killProcess(context.Background(), p, SignalTerm, m.cfg.KillGrace)
		})
		p.mu.Lock()
		p.timer = timer
		p.mu.Unlock()
	}
	go p.reap()

	// If the call was canceled between Start and the result being delivered,
	// do not leave an invisible process behind. The agent will not receive the
	// process ID, so stopping it is the only safe outcome.
	if err := ctx.Err(); err != nil {
		p.requestStop("canceled")
		_ = p.sendSignal(SignalKill)
		return Snapshot{}, err
	}

	return p.snapshot(time.Now()), nil
}

// Spec describes one process start.
type Spec struct {
	Command    string
	WorkingDir string
	Kind       ProcessKind
	MaxRuntime time.Duration
}

// Status returns a snapshot of one process.
func (m *Manager) Status(id string) (Snapshot, error) {
	p, err := m.process(id)
	if err != nil {
		return Snapshot{}, err
	}
	return p.snapshot(time.Now()), nil
}

// List returns snapshots of all tracked processes, oldest first.
func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	processes := make([]*process, 0, len(m.processes))
	for _, p := range m.processes {
		processes = append(processes, p)
	}
	m.mu.Unlock()

	sort.Slice(processes, func(i, j int) bool {
		if processes[i].started.Equal(processes[j].started) {
			return processes[i].id < processes[j].id
		}
		return processes[i].started.Before(processes[j].started)
	})
	now := time.Now()
	snapshots := make([]Snapshot, 0, len(processes))
	for _, p := range processes {
		snap := p.snapshot(now)
		// A list should stay compact; callers that need output can inspect one
		// process or read a cursor.
		snap.Tail = ""
		snapshots = append(snapshots, snap)
	}
	return snapshots
}

// Read returns a bounded chunk of combined output. When wait is positive and
// no output is available at cursor, it waits for new output, process exit, or
// the timeout.
func (m *Manager) Read(ctx context.Context, id string, cursor int64, maxBytes int, wait time.Duration) (Chunk, error) {
	p, err := m.process(id)
	if err != nil {
		return Chunk{}, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultProcessReadBytes
	}
	if maxBytes > m.cfg.MaxReadBytes {
		maxBytes = m.cfg.MaxReadBytes
	}
	return p.read(ctx, cursor, maxBytes, wait), nil
}

// Tail returns the last lines of combined output, capped at maxBytes. When the
// process has produced nothing yet and wait is positive, it waits for output,
// exit, or the timeout.
func (m *Manager) Tail(ctx context.Context, id string, lines, maxBytes int, wait time.Duration) (Chunk, error) {
	p, err := m.process(id)
	if err != nil {
		return Chunk{}, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultProcessReadBytes
	}
	if maxBytes > m.cfg.MaxReadBytes {
		maxBytes = m.cfg.MaxReadBytes
	}
	if lines <= 0 {
		lines = defaultProcessTailLines
	}
	tail := p.combined.TailLines(lines, maxBytes)
	if len(tail.data) == 0 && wait > 0 {
		p.combined.WaitForData(ctx.Done(), 0, wait)
		tail = p.combined.TailLines(lines, maxBytes)
	}
	snap := p.snapshot(time.Now())
	return Chunk{
		ProcessID:    p.id,
		Output:       tail.String(),
		NextCursor:   tail.next,
		Truncated:    tail.truncated,
		OmittedBytes: tail.omitted,
		Status:       snap.Status,
		ExitCode:     snap.ExitCode,
		Signal:       snap.Signal,
		ObservedAt:   snap.ObservedAt,
	}, nil
}

// Wait waits until the process exits or d elapses. stillRunning is true when
// the wait window expired or ctx was canceled.
func (m *Manager) Wait(ctx context.Context, id string, d time.Duration) (Snapshot, bool, error) {
	p, err := m.process(id)
	if err != nil {
		return Snapshot{}, false, err
	}
	if d <= 0 {
		d = m.cfg.DefaultWait
	}
	if d > m.cfg.MaxWait {
		d = m.cfg.MaxWait
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.snapshot(time.Now()), false, nil
	case <-timer.C:
		select {
		case <-p.done:
			return p.snapshot(time.Now()), false, nil
		default:
			return p.snapshot(time.Now()), true, nil
		}
	case <-ctx.Done():
		return p.snapshot(time.Now()), true, ctx.Err()
	}
}

// Promote marks a foreground process as promoted after its wait window
// expired.
func (m *Manager) Promote(id string) (Snapshot, error) {
	p, err := m.process(id)
	if err != nil {
		return Snapshot{}, err
	}
	p.mu.Lock()
	if p.kind == ProcessForeground {
		p.kind = ProcessPromoted
	}
	p.mu.Unlock()
	return p.snapshot(time.Now()), nil
}

// Streams returns bounded stdout and stderr snapshots for the execute result.
func (m *Manager) Streams(id string, maxBytes int) (StreamOutput, error) {
	p, err := m.process(id)
	if err != nil {
		return StreamOutput{}, err
	}
	if maxBytes <= 0 {
		maxBytes = m.cfg.ExecuteBytes
	}
	if maxBytes > m.cfg.ExecuteBytes {
		maxBytes = m.cfg.ExecuteBytes
	}
	return p.streams(maxBytes), nil
}

// Kill stops a process and its process group. confirmed is false when the
// stop was requested but the manager could not observe the exit in time.
func (m *Manager) Kill(ctx context.Context, id string, sig Signal, grace time.Duration) (Snapshot, bool, error) {
	p, err := m.process(id)
	if err != nil {
		return Snapshot{}, false, err
	}
	if !sig.valid() {
		return Snapshot{}, false, fmt.Errorf("%w: %q", ErrInvalidSignal, sig)
	}
	if p.isDone() {
		return p.snapshot(time.Now()), true, nil
	}
	p.requestStop("agent")
	confirmed, err := m.killProcess(ctx, p, sig, grace)
	return p.snapshot(time.Now()), confirmed, err
}

func (m *Manager) killProcess(ctx context.Context, p *process, sig Signal, grace time.Duration) (bool, error) {
	if err := p.sendSignal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return false, err
	}
	if sig != SignalKill {
		if grace <= 0 {
			grace = m.cfg.KillGrace
		}
		timer := time.NewTimer(grace)
		select {
		case <-p.done:
			timer.Stop()
			return true, nil
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err := p.sendSignal(SignalKill); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return false, err
	}
	confirmFor := m.cfg.WaitDelay + time.Second
	if confirmFor < defaultKillConfirm {
		confirmFor = defaultKillConfirm
	}
	confirm := time.NewTimer(confirmFor)
	defer confirm.Stop()
	select {
	case <-p.done:
		return true, nil
	case <-confirm.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Close stops accepting starts, stops every tracked process, and waits for
// them to be reaped. It is safe to call more than once.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	processes := make([]*process, 0, len(m.processes))
	for _, p := range m.processes {
		processes = append(processes, p)
	}
	m.mu.Unlock()

	if len(processes) == 0 {
		return nil
	}
	for _, p := range processes {
		p.requestStop("shutdown")
	}

	var wg sync.WaitGroup
	for _, p := range processes {
		if p.isDone() {
			continue
		}
		wg.Add(1)
		go func(p *process) {
			defer wg.Done()
			_, _ = m.killProcess(ctx, p, SignalTerm, m.cfg.KillGrace)
		}(p)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, p := range processes {
			if !p.isDone() {
				_ = p.sendSignal(SignalKill)
			}
		}
		confirmFor := m.cfg.WaitDelay + time.Second
		if confirmFor < defaultKillConfirm {
			confirmFor = defaultKillConfirm
		}
		timer := time.NewTimer(confirmFor)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
		return ctx.Err()
	}
}

func (m *Manager) process(id string) (*process, error) {
	m.mu.Lock()
	p := m.processes[id]
	m.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("%w: %s", ErrProcessNotFound, id)
	}
	return p, nil
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.processes, id)
	m.mu.Unlock()
}

// pruneFinishedLocked drops the oldest finished records while the finished
// count is over the configured limit. The manager mutex must be held.
func (m *Manager) pruneFinishedLocked() {
	finished := make([]*process, 0, len(m.processes))
	for _, p := range m.processes {
		if p.isDone() {
			finished = append(finished, p)
		}
	}
	over := len(finished) - m.cfg.MaxFinished
	if over <= 0 {
		return
	}
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].started.Before(finished[j].started)
	})
	for i := 0; i < over && i < len(finished); i++ {
		delete(m.processes, finished[i].id)
	}
}

func shellCommand(command string) *osExec.Cmd {
	if runtime.GOOS == "windows" {
		return osExec.Command("cmd.exe", "/c", command)
	}
	return osExec.Command("sh", "-c", command)
}

func newProcessID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("exec: generate process id: %w", err)
	}
	return "proc_" + hex.EncodeToString(raw[:]), nil
}

func resolveWorkDir(base, requested string) (string, error) {
	if requested == "" {
		return base, nil
	}
	if base == "" {
		if filepath.IsAbs(requested) {
			return filepath.Clean(requested), nil
		}
		abs, err := filepath.Abs(requested)
		if err != nil {
			return "", fmt.Errorf("exec: resolve working directory: %w", err)
		}
		return abs, nil
	}
	abs := requested
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(base, requested)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrWorkingDirOutsideBase, requested)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrWorkingDirOutsideBase, requested)
	}
	return abs, nil
}
