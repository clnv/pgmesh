import "toolings.just"

# Generate one sqlc plugin test case.
generate-case case: ensure-sqlc
    go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
    cd "tests/generate/{{ case }}" && {{ sqlc }} generate --file sqlc.yaml

# Generate every sqlc plugin test case.
generate-tests: ensure-sqlc
    go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
    cd tests/generate/same_package && {{ sqlc }} generate --file sqlc.yaml
    cd tests/generate/separate_package && {{ sqlc }} generate --file sqlc.yaml
    cd tests/generate/commands && {{ sqlc }} generate --file sqlc.yaml
