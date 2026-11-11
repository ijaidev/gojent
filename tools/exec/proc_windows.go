//go:build windows

package exec

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// configureProcessGroup is a no-op on Windows: CommandContext already kills
// the direct child, and WaitDelay bounds cleanup for any surviving descendant.
func configureProcessGroup(_ *exec.Cmd) {}

// configureManagedProcessGroup is a no-op on Windows.
func configureManagedProcessGroup(_ *exec.Cmd) {}

// signalProcessGroup stops the command's process tree with taskkill. Console
// signals are not available through the standard library, so TERM and INT are
// best-effort graceful stops and KILL forces the tree.
func signalProcessGroup(cmd *exec.Cmd, sig Signal) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if sig == SignalKill {
		args = append(args, "/F")
	}
	if err := exec.Command("taskkill", args...).Run(); err == nil {
		return nil
	}
	if killErr := cmd.Process.Kill(); killErr != nil {
		if errors.Is(killErr, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return killErr
	}
	return nil
}

// processExitState reports the exit code. Windows has no shell-style signal
// state, so signaled is always false.
func processExitState(ps *os.ProcessState) (code int, name string, signaled bool) {
	if ps == nil {
		return 0, "", false
	}
	return ps.ExitCode(), "", false
}
