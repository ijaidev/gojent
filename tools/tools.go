// Package tools provides readymade, reusable tools for Gojent agents.
package tools

import (
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/tools/exec"
	"github.com/ijaidev/gojent/tools/fs"
	"github.com/ijaidev/gojent/tools/task"
	"github.com/ijaidev/gojent/tools/web"
)

// StandardFilesystemTools returns the core set of filesystem tools (read, write, edit, list).
func StandardFilesystemTools(cfg ...fs.Config) ([]tool.Tool, error) {
	r, err := fs.NewReadFileTool(cfg...)
	if err != nil {
		return nil, err
	}
	w, err := fs.NewWriteFileTool(cfg...)
	if err != nil {
		return nil, err
	}
	e, err := fs.NewEditFileTool(cfg...)
	if err != nil {
		return nil, err
	}
	l, err := fs.NewListFilesTool(cfg...)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{r, w, e, l}, nil
}

// StandardWebTools returns standard web fetching tools.
func StandardWebTools(cfg ...web.FetchConfig) ([]tool.Tool, error) {
	fetchTool, err := web.NewWebFetchTool(cfg...)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{fetchTool}, nil
}

// StandardExecTools returns command execution tools.
func StandardExecTools(cfg ...exec.Config) ([]tool.Tool, error) {
	cmdTool, err := exec.NewExecuteCommandTool(cfg...)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{cmdTool}, nil
}

// StandardTaskTools returns an in-memory task tracker and its associated tools.
func StandardTaskTools() (*task.Tracker, []tool.Tool, error) {
	tracker := task.NewTracker()
	tools, err := tracker.AllTools()
	if err != nil {
		return nil, nil, err
	}
	return tracker, tools, nil
}
