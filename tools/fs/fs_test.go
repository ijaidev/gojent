package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/tools/fs"
)

func TestFSTools_ReadWriteEditList(t *testing.T) {
	tmpDir := t.TempDir()

	writeTool, err := fs.NewWriteFileTool(fs.Config{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create write_file tool: %v", err)
	}

	readTool, err := fs.NewReadFileTool(fs.Config{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create read_file tool: %v", err)
	}

	editTool, err := fs.NewEditFileTool(fs.Config{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create edit_file tool: %v", err)
	}

	listTool, err := fs.NewListFilesTool(fs.Config{BaseDir: tmpDir})
	if err != nil {
		t.Fatalf("failed to create list_files tool: %v", err)
	}

	ctx := context.Background()

	// 1. Write file
	writeInput, _ := json.Marshal(fs.WriteFileInput{
		Path:    "docs/hello.txt",
		Content: "line 1\nline 2\nline 3\nline 4\nline 5",
	})
	resWriteAny, err := writeTool.Execute(ctx, writeInput)
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}
	resWrite := resWriteAny.(fs.WriteFileOutput)
	if !resWrite.Success || resWrite.Bytes == 0 {
		t.Errorf("unexpected write result: %+v", resWrite)
	}

	// 2. Read full file
	readInput, _ := json.Marshal(fs.ReadFileInput{
		Path: "docs/hello.txt",
	})
	resReadAny, err := readTool.Execute(ctx, readInput)
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	resRead := resReadAny.(fs.ReadFileOutput)
	if resRead.TotalLines != 5 {
		t.Errorf("expected 5 lines, got %d", resRead.TotalLines)
	}

	// 3. Read line range
	readRangeInput, _ := json.Marshal(fs.ReadFileInput{
		Path:      "docs/hello.txt",
		StartLine: 2,
		EndLine:   4,
	})
	resRangeAny, err := readTool.Execute(ctx, readRangeInput)
	if err != nil {
		t.Fatalf("read_file range failed: %v", err)
	}
	resRange := resRangeAny.(fs.ReadFileOutput)
	expectedRange := "line 2\nline 3\nline 4"
	if resRange.Content != expectedRange {
		t.Errorf("expected %q, got %q", expectedRange, resRange.Content)
	}

	// 4. Edit file
	editInput, _ := json.Marshal(fs.EditFileInput{
		Path:      "docs/hello.txt",
		OldString: "line 3",
		NewString: "edited line 3",
	})
	resEditAny, err := editTool.Execute(ctx, editInput)
	if err != nil {
		t.Fatalf("edit_file failed: %v", err)
	}
	resEdit := resEditAny.(fs.EditFileOutput)
	if resEdit.Replacements != 1 || !resEdit.Success {
		t.Errorf("unexpected edit result: %+v", resEdit)
	}

	// Verify edit
	data, err := os.ReadFile(filepath.Join(tmpDir, "docs/hello.txt"))
	if err != nil {
		t.Fatalf("failed reading edited file: %v", err)
	}
	if string(data) != "line 1\nline 2\nedited line 3\nline 4\nline 5" {
		t.Errorf("file content after edit incorrect: %s", string(data))
	}

	// 5. List files
	listInput, _ := json.Marshal(fs.ListFilesInput{
		Recursive: true,
	})
	resListAny, err := listTool.Execute(ctx, listInput)
	if err != nil {
		t.Fatalf("list_files failed: %v", err)
	}
	resList := resListAny.(fs.ListFilesOutput)
	if resList.Total < 1 {
		t.Errorf("expected at least 1 entry, got %d", resList.Total)
	}
}

func TestFSTools_DirectoryEscapePrevention(t *testing.T) {
	tmpDir := t.TempDir()
	readTool, _ := fs.NewReadFileTool(fs.Config{BaseDir: tmpDir})

	ctx := context.Background()
	escapeInput, _ := json.Marshal(fs.ReadFileInput{
		Path: "../../etc/passwd",
	})
	_, err := readTool.Execute(ctx, escapeInput)
	if err == nil {
		t.Error("expected error for escaping base directory, got nil")
	}
}
