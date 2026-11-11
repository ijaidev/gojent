//go:build !windows

package exec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup starts the command in its own process group and makes
// context cancellation kill the whole group rather than only the shell. A
// surviving child would otherwise inherit the output pipes and keep Wait
// blocked long after the timeout fired.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}

// configureManagedProcessGroup starts a manager-owned process in its own
// process group. Unlike configureProcessGroup it does not install a Cancel
// function, because the process is deliberately detached from the tool call's
// context.
func configureManagedProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup sends sig to the command's whole process group. ESRCH is
// reported as os.ErrProcessDone so callers can treat an already-exited process
// as a normal race.
func signalProcessGroup(cmd *exec.Cmd, sig Signal) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	var sysSig syscall.Signal
	switch sig {
	case SignalTerm:
		sysSig = syscall.SIGTERM
	case SignalInt:
		sysSig = syscall.SIGINT
	case SignalKill:
		sysSig = syscall.SIGKILL
	default:
		return fmt.Errorf("%w: %q", ErrInvalidSignal, sig)
	}
	if err := syscall.Kill(-cmd.Process.Pid, sysSig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// processExitState reports the exit code, signal name, and whether the process
// was terminated by a signal.
func processExitState(ps *os.ProcessState) (code int, name string, signaled bool) {
	if ps == nil {
		return 0, "", false
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		sig := ws.Signal()
		return 128 + int(sig), signalName(sig), true
	}
	return ps.ExitCode(), "", false
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	default:
		return fmt.Sprintf("SIG%d", int(sig))
	}
}
