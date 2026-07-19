project-root := `git rev-parse --show-toplevel`

export GOCACHE := env_var_or_default("GOCACHE", "/tmp/pgmesh-gocache")
export GOLANGCI_LINT_CACHE := env_var_or_default("GOLANGCI_LINT_CACHE", "/tmp/pgmesh-golangci-lint-cache")
