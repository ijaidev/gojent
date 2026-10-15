# Run all unit tests across the whole package
test:
    go test -v ./...

# Run static analysis and linting across all packages
lint:
    golangci-lint run ./...

# Format all Go source files according to standard Go formatting
fmt:
    go fmt ./...

# Configure git to use repository pre-commit hooks
setup-hooks:
    git config core.hooksPath .githooks
    chmod +x .githooks/pre-commit
