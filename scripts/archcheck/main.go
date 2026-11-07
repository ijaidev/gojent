// Command archcheck enforces Gojent's module and package dependency boundaries.
//
// It parses imports instead of loading packages, so it is fast, works without a
// Go workspace, and never downloads modules. Run it from the repository root:
//
//	go run ./scripts/archcheck
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const modulePath = "github.com/ijaidev/gojent"

type violation struct {
	file       string
	line       int
	from       string
	importPath string
	reason     string
}

func (v violation) String() string {
	return fmt.Sprintf("%s:%d: %s imports %q: %s", v.file, v.line, v.from, v.importPath, v.reason)
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	violations, err := run(*root)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "archcheck: %v\n", err)
		os.Exit(2)
	}
	for _, v := range violations {
		_, _ = fmt.Fprintln(os.Stderr, v)
	}
	if len(violations) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "archcheck: %d violation(s)\n", len(violations))
		os.Exit(1)
	}
	_, _ = fmt.Println("archcheck: dependency boundaries are clean")
}

// run walks root, parses each Go file's imports, and returns every boundary
// violation it finds. It does not execute or type-check the code.
func run(root string) ([]violation, error) {
	var violations []violation

	requires, line, err := rootGoModRequires(root)
	if err != nil {
		return nil, err
	}
	if requires {
		violations = append(violations, violation{
			file:       "go.mod",
			line:       line,
			from:       "root",
			importPath: "third-party module",
			reason:     "the root module must remain standard-library only",
		})
	}

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipDir(path, entry) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		from := areaFor(rel)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: unquote import %s: %w", rel, spec.Path.Value, err)
			}
			if isThirdParty(importPath) {
				if stdlibOnly(from) {
					violations = append(violations, violation{
						file:       rel,
						line:       fset.Position(spec.Pos()).Line,
						from:       from,
						importPath: importPath,
						reason:     "this package must stay standard-library only",
					})
				}
				continue
			}
			to, ok := importArea(importPath)
			if !ok || allowed(from, to) {
				continue
			}
			violations = append(violations, violation{
				file:       rel,
				line:       fset.Position(spec.Pos()).Line,
				from:       from,
				importPath: importPath,
				reason:     fmt.Sprintf("dependency direction %s -> %s is not allowed", from, to),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].line < violations[j].line
	})
	return violations, nil
}

// skipDir keeps vendored, generated, and fixture trees out of the scan.
func skipDir(path string, entry fs.DirEntry) bool {
	switch entry.Name() {
	case ".git", ".agents", "node_modules", "vendor", ".local", "testdata":
		return true
	}
	return entry.Name() == "out" && strings.Contains(filepath.ToSlash(path), "/frontend/")
}

// areaFor maps a repository-relative file path to the boundary it belongs to.
func areaFor(rel string) string {
	switch {
	case strings.HasPrefix(rel, "core/"):
		return "core"
	case strings.HasPrefix(rel, "providers/internal/"):
		return "providers/internal"
	case strings.HasPrefix(rel, "providers/"):
		return "provider:" + pathSegment(rel, "providers/")
	case strings.HasPrefix(rel, "stores/"):
		return "store:" + pathSegment(rel, "stores/")
	case strings.HasPrefix(rel, "tools/"):
		return "tools"
	case strings.HasPrefix(rel, "codjent/internal/app/"):
		return "codjent/app"
	case strings.HasPrefix(rel, "codjent/internal/httpapi/"):
		return "codjent/httpapi"
	case strings.HasPrefix(rel, "codjent/internal/provider/"):
		return "codjent/provider"
	case strings.HasPrefix(rel, "codjent/internal/store/"):
		return "codjent/store"
	case strings.HasPrefix(rel, "codjent/cmd/"):
		return "codjent/cmd"
	case strings.HasPrefix(rel, "codjent/"):
		return "codjent/other"
	case strings.HasPrefix(rel, "examples/"):
		return "example"
	default:
		return "root"
	}
}

// importArea maps a module import path to the boundary it belongs to. The bool
// is false for imports outside this repository.
func importArea(importPath string) (string, bool) {
	if importPath == modulePath {
		return "root", true
	}
	if !strings.HasPrefix(importPath, modulePath+"/") {
		return "", false
	}

	rest := strings.TrimPrefix(importPath, modulePath+"/")
	switch {
	case rest == "core" || strings.HasPrefix(rest, "core/"):
		return "core", true
	case rest == "providers":
		return "providers", true
	case strings.HasPrefix(rest, "providers/internal/"):
		return "providers/internal", true
	case strings.HasPrefix(rest, "providers/"):
		return "provider:" + firstSegment(strings.TrimPrefix(rest, "providers/")), true
	case rest == "stores":
		return "stores", true
	case strings.HasPrefix(rest, "stores/"):
		return "store:" + firstSegment(strings.TrimPrefix(rest, "stores/")), true
	case rest == "tools" || strings.HasPrefix(rest, "tools/"):
		return "tools", true
	case rest == "codjent/internal/app" || strings.HasPrefix(rest, "codjent/internal/app/"):
		return "codjent/app", true
	case rest == "codjent/internal/httpapi" || strings.HasPrefix(rest, "codjent/internal/httpapi/"):
		return "codjent/httpapi", true
	case rest == "codjent/internal/provider" || strings.HasPrefix(rest, "codjent/internal/provider/"):
		return "codjent/provider", true
	case rest == "codjent/internal/store" || strings.HasPrefix(rest, "codjent/internal/store/"):
		return "codjent/store", true
	case rest == "codjent/cmd" || strings.HasPrefix(rest, "codjent/cmd/"):
		return "codjent/cmd", true
	case rest == "codjent" || strings.HasPrefix(rest, "codjent/"):
		return "codjent/other", true
	case rest == "examples" || strings.HasPrefix(rest, "examples/"):
		return "example", true
	default:
		return "root", true
	}
}

// allowed reports whether an import from one boundary to another is permitted.
// Third-party imports are handled separately by stdlibOnly.
func allowed(from, to string) bool {
	if from == to {
		return true
	}
	if strings.HasPrefix(from, "provider:") {
		return to == "core" || to == "providers/internal"
	}
	if strings.HasPrefix(from, "store:") {
		return to == "core"
	}

	switch from {
	case "core":
		return to == "core"
	case "tools":
		return to == "core"
	case "providers/internal":
		return to == "core" || to == "providers/internal"
	case "codjent/app":
		return to != "codjent/httpapi" && to != "codjent/cmd"
	case "codjent/httpapi":
		return to != "codjent/cmd"
	case "codjent/provider", "codjent/store":
		// Concrete stores and provider catalogs may import app types and the
		// app.Store surface they implement. They must not reach upward into
		// HTTP or the command root.
		return to != "codjent/httpapi" && to != "codjent/cmd"
	case "codjent/cmd", "codjent/other", "example", "root":
		return true
	default:
		return false
	}
}

// stdlibOnly reports whether the boundary may import third-party modules.
// providers/mockllm is the only provider that lives in the root module.
func stdlibOnly(area string) bool {
	switch area {
	case "core", "tools", "root", "providers/internal", "provider:mockllm":
		return true
	default:
		return false
	}
}

func isThirdParty(importPath string) bool {
	if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
		return false
	}
	return strings.Contains(firstSegment(importPath), ".")
}

// rootGoModRequires reports whether the root go.mod declares a require block.
func rootGoModRequires(root string) (required bool, line int, err error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0, fmt.Errorf("read %s: %w", path, err)
	}
	for index, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "require") {
			return true, index + 1, nil
		}
	}
	return false, 0, nil
}

func pathSegment(rel, prefix string) string {
	return firstSegment(strings.TrimPrefix(rel, prefix))
}

func firstSegment(value string) string {
	if index := strings.IndexByte(value, '/'); index >= 0 {
		return value[:index]
	}
	return value
}
