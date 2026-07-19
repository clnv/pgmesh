import "toolings.just"

# Generate the integration fixture.
generate-fixture: ensure-sqlc
    go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
    cd integration/fixture && {{ sqlc }} generate --file sqlc.yaml

# Generate the copyable example package.
generate-example: generate-fixture
    cd examples/sqlc && {{ sqlc }} generate --file sqlc.yaml
