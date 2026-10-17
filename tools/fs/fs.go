// Package fs provides readymade filesystem tools for reading, writing, editing, and listing files.
package fs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ijaidev/gojent/core/tool"
)

// Config configures filesystem tools, optionally sandboxing operations to BaseDir.
type Config struct {
	BaseDir string
}

func resolvePath(baseDir, path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if baseDir == "" {
		return cleanPath, nil
	}
	cleanBase := filepath.Clean(baseDir)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(cleanBase, cleanPath)
	}
	rel, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("fs: path %q escapes base directory %q", path, baseDir)
	}
	return cleanPath, nil
}

// ReadFileInput defines the parameters for reading a file.
type ReadFileInput struct {
	Path      string `json:"path" description:"Relative or absolute path of the file to read"`
	StartLine int    `json:"start_line,omitempty" description:"Optional 1-based start line number"`
	EndLine   int    `json:"end_line,omitempty" description:"Optional 1-based end line number"`
}

// ReadFileOutput represents the content returned from reading a file.
type ReadFileOutput struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
}

// NewReadFileTool creates a Tool for reading file contents with optional line range filtering.
func NewReadFileTool(cfg ...Config) (tool.Tool, error) {
	var baseDir string
	if len(cfg) > 0 {
		baseDir = cfg[0].BaseDir
	}

	return tool.New(tool.Config[ReadFileInput, ReadFileOutput]{
		ID:          "read_file",
		Description: "Read content from a file, optionally specifying start and end line numbers.",
		Execute: func(_ context.Context, in ReadFileInput) (ReadFileOutput, error) {
			target, err := resolvePath(baseDir, in.Path)
			if err != nil {
				return ReadFileOutput{}, err
			}

			file, err := os.Open(target)
			if err != nil {
				return ReadFileOutput{}, fmt.Errorf("read_file: %w", err)
			}
			defer func() {
				_ = file.Close()
			}()

			scanner := bufio.NewScanner(file)
			var lines []string
			currentLine := 0

			for scanner.Scan() {
				currentLine++
				if in.StartLine > 0 && currentLine < in.StartLine {
					continue
				}
				if in.EndLine > 0 && currentLine > in.EndLine {
					continue
				}
				lines = append(lines, scanner.Text())
			}

			if err := scanner.Err(); err != nil {
				return ReadFileOutput{}, fmt.Errorf("read_file: error reading file: %w", err)
			}

			return ReadFileOutput{
				Path:       in.Path,
				Content:    strings.Join(lines, "\n"),
				TotalLines: currentLine,
			}, nil
		},
	})
}

// WriteFileInput defines the parameters for writing a file.
type WriteFileInput struct {
	Path    string `json:"path" description:"Path of the file to write"`
	Content string `json:"content" description:"Content to write to the file"`
}

// WriteFileOutput represents the outcome of a file write.
type WriteFileOutput struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Success bool   `json:"success"`
}

// NewWriteFileTool creates a Tool for creating or overwriting a file.
func NewWriteFileTool(cfg ...Config) (tool.Tool, error) {
	var baseDir string
	if len(cfg) > 0 {
		baseDir = cfg[0].BaseDir
	}

	return tool.New(tool.Config[WriteFileInput, WriteFileOutput]{
		ID:          "write_file",
		Description: "Create or overwrite a file with the specified content, automatically creating parent directories.",
		Execute: func(_ context.Context, in WriteFileInput) (WriteFileOutput, error) {
			target, err := resolvePath(baseDir, in.Path)
			if err != nil {
				return WriteFileOutput{}, err
			}

			dir := filepath.Dir(target)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return WriteFileOutput{}, fmt.Errorf("write_file: cannot create directory: %w", err)
			}

			data := []byte(in.Content)
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return WriteFileOutput{}, fmt.Errorf("write_file: %w", err)
			}

			return WriteFileOutput{
				Path:    in.Path,
				Bytes:   len(data),
				Success: true,
			}, nil
		},
	})
}

// EditFileInput defines parameters for replacing text in an existing file.
type EditFileInput struct {
	Path      string `json:"path" description:"Path of the file to edit"`
	OldString string `json:"old_string" description:"Exact text substring to replace"`
	NewString string `json:"new_string" description:"Replacement text"`
}

// EditFileOutput represents the result of editing a file.
type EditFileOutput struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	Success      bool   `json:"success"`
}

// NewEditFileTool creates a Tool for finding and replacing strings within a file.
func NewEditFileTool(cfg ...Config) (tool.Tool, error) {
	var baseDir string
	if len(cfg) > 0 {
		baseDir = cfg[0].BaseDir
	}

	return tool.New(tool.Config[EditFileInput, EditFileOutput]{
		ID:          "edit_file",
		Description: "Edit an existing file by replacing occurrences of old_string with new_string.",
		Execute: func(_ context.Context, in EditFileInput) (EditFileOutput, error) {
			target, err := resolvePath(baseDir, in.Path)
			if err != nil {
				return EditFileOutput{}, err
			}

			data, err := os.ReadFile(target)
			if err != nil {
				return EditFileOutput{}, fmt.Errorf("edit_file: %w", err)
			}

			content := string(data)
			if !strings.Contains(content, in.OldString) {
				return EditFileOutput{}, fmt.Errorf("edit_file: old_string not found in file %q", in.Path)
			}

			count := strings.Count(content, in.OldString)
			newContent := strings.ReplaceAll(content, in.OldString, in.NewString)

			if err := os.WriteFile(target, []byte(newContent), 0o644); err != nil {
				return EditFileOutput{}, fmt.Errorf("edit_file: write failed: %w", err)
			}

			return EditFileOutput{
				Path:         in.Path,
				Replacements: count,
				Success:      true,
			}, nil
		},
	})
}

// FileEntry describes an entry in a directory.
type FileEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ListFilesInput defines parameters for directory listing.
type ListFilesInput struct {
	Path      string `json:"path,omitempty" description:"Directory path to list (defaults to current directory or base dir)"`
	Recursive bool   `json:"recursive,omitempty" description:"Whether to recursively list all files and subdirectories"`
	MaxFiles  int    `json:"max_files,omitempty" description:"Maximum number of files to return (defaults to 100)"`
}

// ListFilesOutput represents directory entries.
type ListFilesOutput struct {
	Files []FileEntry `json:"files"`
	Total int         `json:"total"`
}

// NewListFilesTool creates a Tool for inspecting files and folders.
func NewListFilesTool(cfg ...Config) (tool.Tool, error) {
	var baseDir string
	if len(cfg) > 0 {
		baseDir = cfg[0].BaseDir
	}

	return tool.New(tool.Config[ListFilesInput, ListFilesOutput]{
		ID:          "list_files",
		Description: "List files and directories at a path, optionally with recursion.",
		Execute: func(_ context.Context, in ListFilesInput) (ListFilesOutput, error) {
			path := in.Path
			if path == "" {
				path = "."
			}
			target, err := resolvePath(baseDir, path)
			if err != nil {
				return ListFilesOutput{}, err
			}

			maxFiles := in.MaxFiles
			if maxFiles <= 0 {
				maxFiles = 100
			}

			var entries []FileEntry
			if in.Recursive {
				err = filepath.WalkDir(target, func(p string, d fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if p == target {
						return nil
					}
					if len(entries) >= maxFiles {
						return fs.SkipAll
					}
					rel, relErr := filepath.Rel(target, p)
					if relErr != nil {
						rel = p
					}
					info, _ := d.Info()
					size := int64(0)
					if info != nil {
						size = info.Size()
					}
					entries = append(entries, FileEntry{
						Path:  rel,
						IsDir: d.IsDir(),
						Size:  size,
					})
					return nil
				})
			} else {
				dirEntries, readErr := os.ReadDir(target)
				if readErr != nil {
					return ListFilesOutput{}, fmt.Errorf("list_files: %w", readErr)
				}
				for _, d := range dirEntries {
					if len(entries) >= maxFiles {
						break
					}
					info, _ := d.Info()
					size := int64(0)
					if info != nil {
						size = info.Size()
					}
					entries = append(entries, FileEntry{
						Path:  d.Name(),
						IsDir: d.IsDir(),
						Size:  size,
					})
				}
			}

			if err != nil && !errors.Is(err, fs.SkipAll) {
				return ListFilesOutput{}, fmt.Errorf("list_files: %w", err)
			}

			return ListFilesOutput{
				Files: entries,
				Total: len(entries),
			}, nil
		},
	})
}
