# Run all unit tests across the whole package
test:
    go test -v ./... ./providers/google/... ./examples/google/... ./stores/inmemory/... ./stores/sqlite/...

# Run static analysis and linting across all packages
lint:
    golangci-lint run ./... ./providers/google/... ./examples/google/... ./stores/inmemory/... ./stores/sqlite/...

# Format all Go source files according to standard Go formatting
fmt:
    gofmt -s -w .

# Run all checks (tidy check, lint, and unit tests)
check: tidy-check lint test

# Tidy all go.mod and go.sum files across all modules
tidy:
    #!/usr/bin/env bash
    set -e
    go mod tidy
    go work sync
    echo "Workspace and modules tidied successfully."

# Check that all go.mod files across all modules are tidy
tidy-check:
    #!/usr/bin/env bash
    set -e
    root_diff=$(go mod tidy -diff 2>&1 || true)
    if [ -n "$root_diff" ]; then
        echo "[ERROR] Root module is not tidy:"
        echo "$root_diff"
        exit 1
    fi
    go work sync
    sync_diff=$(git diff --name-only "*go.mod" "*go.sum" "go.work.sum")
    if [ -n "$sync_diff" ]; then
        echo "[ERROR] Workspace modules are out of sync:"
        echo "$sync_diff"
        exit 1
    fi
    echo "All modules and workspace are tidy and in sync."

# Alias check-tidy to tidy-check
alias check-tidy := tidy-check

# Configure git to use repository pre-commit hooks
setup-hooks:
    git config core.hooksPath .githooks
    chmod +x .githooks/pre-commit
