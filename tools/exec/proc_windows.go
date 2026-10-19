//go:build windows

package exec

import "os/exec"

// configureProcessGroup is a no-op on Windows: CommandContext already kills
// the direct child, and WaitDelay bounds cleanup for any surviving descendant.
func configureProcessGroup(_ *exec.Cmd) {}
