//go:build !windows

package exec

import (
	"errors"
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
