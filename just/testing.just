import "generation.just"

# Run unit tests.
test:
    go test ./...

# Run static analysis from the Go toolchain.
vet:
    go vet ./...

# Run golangci-lint.
lint: ensure-golangci-lint
    {{ golangci-lint }} run --config {{ project-root }}/.golangci.yaml --timeout=5m ./...

# Regenerate code and run all non-Docker checks.
verify-unit: generate-tests
    just --justfile examples/justfile generate
    just test
    just vet
    just lint
