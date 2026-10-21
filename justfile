# Run all unit tests across the whole package
test:
    go test -v ./... ./providers/google/... ./examples/google/... ./stores/postgres/... ./stores/sqlite/... ./codjent/...

# Run the database integration tests (requires GOJENT_TEST_POSTGRES_DSN)
test-integration:
    go test -race -v ./stores/postgres/... -run TestStore_Integration

# Run static analysis and linting across all packages
lint:
    golangci-lint run ./... ./providers/google/... ./examples/google/... ./stores/postgres/... ./stores/sqlite/... ./codjent/...

# Run codjent's local backend
codjent:
    go run ./codjent/cmd/codjent

# Format all Go source files according to standard Go formatting
fmt:
    gofmt -s -w .

# Check that all Go source files are formatted according to standard Go formatting
fmt-check:
    #!/usr/bin/env bash
    set -e
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
        echo "[ERROR] The following files are not formatted:"
        echo "$unformatted"
        echo ""
        echo "Please run 'just fmt' or 'gofmt -s -w .' to format your code before committing."
        exit 1
    fi
    echo "Formatting check passed."

# Run all checks (formatting check, tidy check, lint, and unit tests)
check: fmt-check tidy-check lint test


# Tidy all go.mod and go.sum files across all modules
tidy:
    #!/usr/bin/env bash
    set -e
    for dir in . providers/google examples/google stores/sqlite stores/postgres codjent; do
        echo "== $dir =="
        (cd "$dir" && go mod tidy)
    done
    go work sync
    echo "Workspace and modules tidied successfully."

# Check that all go.mod files across all modules are tidy
tidy-check:
    #!/usr/bin/env bash
    set -e
    for dir in . providers/google examples/google stores/sqlite stores/postgres codjent; do
        diff=$(cd "$dir" && go mod tidy -diff 2>&1 || true)
        if [ -n "$diff" ]; then
            echo "[ERROR] Module $dir is not tidy:"
            echo "$diff"
            exit 1
        fi
    done
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

# Alias pre-commit to check
alias pre-commit := check

# Configure git to use repository pre-commit hooks
setup-hooks:
    git config core.hooksPath .githooks
    chmod +x .githooks/pre-commit
