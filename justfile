# Non-root modules. Keep in sync with the go.work use directives.
modules := "codjent examples/google providers/anthropic providers/google providers/openai stores/postgres stores/sqlite"

# Create the local Go workspace (go.work is not tracked)
workspace:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f go.work ]; then
        go work init
    fi
    go work use . {{modules}}

# Run unit tests for the root module and every other module
test:
    #!/usr/bin/env bash
    set -euo pipefail
    go test -race ./...
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" test
    done

# Run go vet for the root module and every other module
vet:
    #!/usr/bin/env bash
    set -euo pipefail
    go vet ./...
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" vet
    done

# Build the root module and every other module
build:
    #!/usr/bin/env bash
    set -euo pipefail
    go build ./...
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" build
    done

# Run static analysis for the root module and every other module
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    golangci-lint run ./...
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" lint
    done

# Tidy go.mod and go.sum in every module
tidy:
    #!/usr/bin/env bash
    set -euo pipefail
    go mod tidy
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" tidy
    done

# Check that every module's go.mod and go.sum are tidy
tidy-check:
    #!/usr/bin/env bash
    set -euo pipefail
    go mod tidy -diff
    for dir in {{modules}}; do
        echo "== $dir =="
        just --justfile "$dir/justfile" tidy-check
    done

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

# Verify codjent's embedded frontend export is current
frontend-check:
    just --justfile codjent/justfile frontend-check

# Run all checks (formatting, tidy, vet, build, lint, tests, and frontend)
check: fmt-check tidy-check vet build lint test frontend-check
