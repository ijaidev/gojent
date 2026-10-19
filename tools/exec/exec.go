// Package exec provides readymade tools for executing commands and shell scripts.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ijaidev/gojent/core/tool"
)

const (
	defaultTimeout        = 30 * time.Second
	maxAllowedTimeout     = 300 * time.Second
	defaultMaxOutputBytes = 1024 * 1024 // 1MB

	// waitDelay bounds how long Wait blocks for the output pipes to close
	// after the process exits or is canceled. Without it, orphaned
	// descendants that inherited the pipes can hang the tool far past its
	// timeout.
	waitDelay = 5 * time.Second
)

// Config configures the command execution tool.
type Config struct {
	BaseDir        string
	DefaultTimeout time.Duration
	MaxOutputBytes int
}

// CommandInput defines the input parameters for executing a shell command.
type CommandInput struct {
	Command        string `json:"command" description:"The command line string to execute"`
	WorkingDir     string `json:"working_dir,omitempty" description:"Optional working directory for the command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" description:"Optional execution timeout in seconds (default 30, max 300)"`
}

// CommandOutput represents the execution result.
type CommandOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	remaining := l.max - l.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, err := l.buf.Write(p[:remaining])
		return len(p), err
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string {
	return l.buf.String()
}

// NewExecuteCommandTool creates a Tool for executing system commands.
func NewExecuteCommandTool(cfg ...Config) (tool.Tool, error) {
	var c Config
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = defaultTimeout
	}
	if c.MaxOutputBytes <= 0 {
		c.MaxOutputBytes = defaultMaxOutputBytes
	}

	return tool.New(tool.Config[CommandInput, CommandOutput]{
		ID:          "execute_command",
		Description: "Execute a shell command with a timeout, capturing standard output, error, and exit code.",
		Execute: func(ctx context.Context, in CommandInput) (CommandOutput, error) {
			if in.Command == "" {
				return CommandOutput{}, fmt.Errorf("execute_command: command cannot be empty")
			}

			timeout := c.DefaultTimeout
			if in.TimeoutSeconds > 0 {
				reqTimeout := time.Duration(in.TimeoutSeconds) * time.Second
				if reqTimeout > maxAllowedTimeout {
					timeout = maxAllowedTimeout
				} else {
					timeout = reqTimeout
				}
			}

			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(execCtx, "cmd.exe", "/c", in.Command)
			} else {
				cmd = exec.CommandContext(execCtx, "sh", "-c", in.Command)
			}

			// Kill the whole process group on cancellation so orphaned
			// children cannot keep the output pipes open; WaitDelay caps the
			// wait for any descendant that still escapes.
			configureProcessGroup(cmd)
			cmd.WaitDelay = waitDelay

			workDir := c.BaseDir
			if in.WorkingDir != "" {
				if filepath.IsAbs(in.WorkingDir) || c.BaseDir == "" {
					workDir = in.WorkingDir
				} else {
					workDir = filepath.Join(c.BaseDir, in.WorkingDir)
				}
			}
			if workDir != "" {
				cmd.Dir = workDir
			}

			stdoutBuf := &limitedBuffer{max: c.MaxOutputBytes}
			stderrBuf := &limitedBuffer{max: c.MaxOutputBytes}
			cmd.Stdout = stdoutBuf
			cmd.Stderr = stderrBuf

			runErr := cmd.Run()
			timedOut := errors.Is(execCtx.Err(), context.DeadlineExceeded)

			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				switch {
				case errors.As(runErr, &exitErr):
					exitCode = exitErr.ExitCode()
				case timedOut:
					exitCode = -1
				default:
					return CommandOutput{}, fmt.Errorf("execute_command: start failed: %w", runErr)
				}
			}

			return CommandOutput{
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				ExitCode: exitCode,
				TimedOut: timedOut,
			}, nil
		},
	})
}
