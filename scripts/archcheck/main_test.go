package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAreaFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "core", path: "core/llm/llm.go", want: "core"},
		{name: "tools", path: "tools/fs/fs.go", want: "tools"},
		{name: "root mock provider", path: "providers/mockllm/mockllm.go", want: "provider:mockllm"},
		{name: "shared provider helper", path: "providers/internal/httpheader/httpheader.go", want: "providers/internal"},
		{name: "external provider", path: "providers/anthropic/anthropic.go", want: "provider:anthropic"},
		{name: "store", path: "stores/sqlite/sqlite.go", want: "store:sqlite"},
		{name: "codjent app", path: "codjent/internal/app/app.go", want: "codjent/app"},
		{name: "codjent httpapi", path: "codjent/internal/httpapi/api.go", want: "codjent/httpapi"},
		{name: "codjent provider", path: "codjent/internal/provider/provider.go", want: "codjent/provider"},
		{name: "codjent store", path: "codjent/internal/store/sqlite/store.go", want: "codjent/store"},
		{name: "codjent command", path: "codjent/cmd/codjent/main.go", want: "codjent/cmd"},
		{name: "codjent config", path: "codjent/internal/config/config.go", want: "codjent/other"},
		{name: "example", path: "examples/google/main.go", want: "example"},
		{name: "repository script", path: "scripts/archcheck/main.go", want: "root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := areaFor(tt.path); got != tt.want {
				t.Errorf("areaFor(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestImportArea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		importPath string
		want       string
		wantOK     bool
	}{
		{name: "core", importPath: "github.com/ijaidev/gojent/core/llm", want: "core", wantOK: true},
		{name: "provider", importPath: "github.com/ijaidev/gojent/providers/anthropic", want: "provider:anthropic", wantOK: true},
		{name: "shared provider helper", importPath: "github.com/ijaidev/gojent/providers/internal/httpheader", want: "providers/internal", wantOK: true},
		{name: "store", importPath: "github.com/ijaidev/gojent/stores/sqlite", want: "store:sqlite", wantOK: true},
		{name: "tools", importPath: "github.com/ijaidev/gojent/tools/fs", want: "tools", wantOK: true},
		{name: "codjent app", importPath: "github.com/ijaidev/gojent/codjent/internal/app", want: "codjent/app", wantOK: true},
		{name: "codjent command", importPath: "github.com/ijaidev/gojent/codjent/cmd/codjent", want: "codjent/cmd", wantOK: true},
		{name: "example", importPath: "github.com/ijaidev/gojent/examples/google", want: "example", wantOK: true},
		{name: "standard library", importPath: "net/http", want: "", wantOK: false},
		{name: "third party", importPath: "github.com/stretchr/testify/assert", want: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := importArea(tt.importPath)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("importArea(%q) = (%q, %t), want (%q, %t)", tt.importPath, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "core may import core", from: "core", to: "core", want: true},
		{name: "core may not import tools", from: "core", to: "tools", want: false},
		{name: "tools may import core", from: "tools", to: "core", want: true},
		{name: "tools may not import a provider", from: "tools", to: "provider:mockllm", want: false},
		{name: "provider may import core", from: "provider:openai", to: "core", want: true},
		{name: "provider may import shared helpers", from: "provider:openai", to: "providers/internal", want: true},
		{name: "provider may import itself", from: "provider:openai", to: "provider:openai", want: true},
		{name: "provider may not import another provider", from: "provider:openai", to: "provider:anthropic", want: false},
		{name: "store may import core", from: "store:sqlite", to: "core", want: true},
		{name: "store may not import another store", from: "store:sqlite", to: "store:postgres", want: false},
		{name: "shared helpers may import core", from: "providers/internal", to: "core", want: true},
		{name: "app may not import httpapi", from: "codjent/app", to: "codjent/httpapi", want: false},
		{name: "app may import provider", from: "codjent/app", to: "codjent/provider", want: true},
		{name: "app may import tools", from: "codjent/app", to: "tools", want: true},
		{name: "httpapi may import app", from: "codjent/httpapi", to: "codjent/app", want: true},
		{name: "httpapi may not import cmd", from: "codjent/httpapi", to: "codjent/cmd", want: false},
		{name: "provider may import app types", from: "codjent/provider", to: "codjent/app", want: true},
		{name: "provider may not import httpapi", from: "codjent/provider", to: "codjent/httpapi", want: false},
		{name: "store may import app types", from: "codjent/store", to: "codjent/app", want: true},
		{name: "store may not import cmd", from: "codjent/store", to: "codjent/cmd", want: false},
		{name: "cmd may import httpapi", from: "codjent/cmd", to: "codjent/httpapi", want: true},
		{name: "example may import a provider", from: "example", to: "provider:openai", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := allowed(tt.from, tt.to); got != tt.want {
				t.Errorf("allowed(%q, %q) = %t, want %t", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestStdlibOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		area string
		want bool
	}{
		{name: "core", area: "core", want: true},
		{name: "tools", area: "tools", want: true},
		{name: "root", area: "root", want: true},
		{name: "shared provider helpers", area: "providers/internal", want: true},
		{name: "root mock provider", area: "provider:mockllm", want: true},
		{name: "external provider", area: "provider:openai", want: false},
		{name: "store", area: "store:sqlite", want: false},
		{name: "codjent app", area: "codjent/app", want: false},
		{name: "example", area: "example", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stdlibOnly(tt.area); got != tt.want {
				t.Errorf("stdlibOnly(%q) = %t, want %t", tt.area, got, tt.want)
			}
		})
	}
}

func TestIsThirdParty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		importPath string
		want       bool
	}{
		{name: "standard library", importPath: "net/http", want: false},
		{name: "context", importPath: "context", want: false},
		{name: "this module", importPath: "github.com/ijaidev/gojent/core/llm", want: false},
		{name: "provider SDK", importPath: "github.com/openai/openai-go", want: true},
		{name: "golang.org module", importPath: "golang.org/x/sync/errgroup", want: true},
		{name: "other module", importPath: "example.com/foo", want: true},
		{name: "cgo", importPath: "C", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isThirdParty(tt.importPath); got != tt.want {
				t.Errorf("isThirdParty(%q) = %t, want %t", tt.importPath, got, tt.want)
			}
		})
	}
}

func TestRunAcceptsCleanTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module github.com/ijaidev/gojent\n\ngo 1.25.5\n")
	writeFile(t, root, "core/llm/llm.go", "package llm\n")
	writeFile(t, root, "core/tool/tool.go", `package tool
import _ "github.com/ijaidev/gojent/core/llm"
`)
	writeFile(t, root, "core/agent/agent.go", `package agent
import (
	_ "github.com/ijaidev/gojent/core/llm"
	_ "github.com/ijaidev/gojent/core/session"
	_ "github.com/ijaidev/gojent/core/tool"
)
`)
	writeFile(t, root, "tools/fs/fs.go", `package fs
import _ "github.com/ijaidev/gojent/core/tool"
`)
	writeFile(t, root, "providers/internal/httpheader/httpheader.go", "package httpheader\n")
	writeFile(t, root, "providers/mockllm/mockllm.go", `package mockllm
import _ "github.com/ijaidev/gojent/core/llm"
`)
	writeFile(t, root, "providers/openai/openai.go", `package openai
import (
	_ "github.com/ijaidev/gojent/core/llm"
	_ "github.com/openai/openai-go"
)
`)
	writeFile(t, root, "stores/sqlite/sqlite.go", `package sqlite
import (
	_ "github.com/ijaidev/gojent/core/session"
	_ "github.com/mattn/go-sqlite3"
)
`)
	writeFile(t, root, "codjent/internal/app/app.go", `package app
import (
	_ "github.com/ijaidev/gojent/codjent/internal/provider"
	_ "github.com/ijaidev/gojent/core/llm"
	_ "github.com/ijaidev/gojent/tools/fs"
)
`)
	writeFile(t, root, "codjent/internal/provider/provider.go", `package provider
import (
	_ "github.com/ijaidev/gojent/core/llm"
	_ "github.com/ijaidev/gojent/providers/openai"
)
`)
	writeFile(t, root, "codjent/internal/httpapi/api.go", `package httpapi
import (
	_ "github.com/ijaidev/gojent/codjent/internal/app"
	_ "github.com/ijaidev/gojent/codjent/internal/provider"
)
`)
	writeFile(t, root, "codjent/cmd/codjent/main.go", `package main
import (
	_ "github.com/ijaidev/gojent/codjent/internal/app"
	_ "github.com/ijaidev/gojent/codjent/internal/httpapi"
)
`)
	writeFile(t, root, "examples/google/main.go", `package main
import _ "github.com/ijaidev/gojent/providers/google"
`)

	violations, err := run(root)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("run() violations = %d, want 0:\n%s", len(violations), joinViolations(violations))
	}
}

func TestRunReportsImportViolations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module github.com/ijaidev/gojent\n\ngo 1.25.5\n")
	writeFile(t, root, "core/bad.go", `package core
import _ "github.com/ijaidev/gojent/providers/openai"
`)
	writeFile(t, root, "core/third_party.go", `package core
import _ "github.com/stretchr/testify/assert"
`)
	writeFile(t, root, "tools/bad.go", `package tools
import _ "github.com/ijaidev/gojent/stores/sqlite"
`)
	writeFile(t, root, "providers/openai/bad.go", `package openai
import _ "github.com/ijaidev/gojent/providers/anthropic"
`)
	writeFile(t, root, "codjent/internal/app/bad.go", `package app
import _ "github.com/ijaidev/gojent/codjent/internal/httpapi"
`)
	writeFile(t, root, "codjent/internal/httpapi/bad.go", `package httpapi
import _ "github.com/ijaidev/gojent/codjent/cmd"
`)
	writeFile(t, root, "codjent/internal/store/bad.go", `package store
import _ "github.com/ijaidev/gojent/codjent/internal/httpapi"
`)

	violations, err := run(root)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(violations) != 7 {
		t.Fatalf("run() violations = %d, want 7:\n%s", len(violations), joinViolations(violations))
	}
	joined := joinViolations(violations)
	for _, want := range []string{
		"core/bad.go",
		"core/third_party.go",
		"tools/bad.go",
		"providers/openai/bad.go",
		"codjent/internal/app/bad.go",
		"codjent/internal/httpapi/bad.go",
		"codjent/internal/store/bad.go",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("violations do not mention %s:\n%s", want, joined)
		}
	}
}

func TestRunReportsRootRequire(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "go.mod", `module github.com/ijaidev/gojent

go 1.25.5

require github.com/stretchr/testify v1.10.0
`)
	writeFile(t, root, "core/ok.go", "package core\n")

	violations, err := run(root)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("run() violations = %d, want 1:\n%s", len(violations), joinViolations(violations))
	}
	if violations[0].file != "go.mod" {
		t.Errorf("violation file = %q, want go.mod", violations[0].file)
	}
	if !strings.Contains(violations[0].reason, "standard-library only") {
		t.Errorf("violation reason = %q, want the standard-library rule", violations[0].reason)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func joinViolations(violations []violation) string {
	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, v.String())
	}
	return strings.Join(lines, "\n")
}
