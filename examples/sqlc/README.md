# sqlc process-plugin example

This directory is a minimal PostgreSQL/sqlc project showing the annotation
grammar and process-plugin configuration. From the `pgmesh` module:

```bash
GOWORK=off go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
cd examples/sqlc
sqlc generate --file sqlc.yaml
```

The checked-in generated package at `../internal/db` contains node-level
read/write wrappers and a typed `ShardedQueries[SK]` facade. `ListAccounts` has
no shard annotation, so it is available only through the node-level wrappers.
`CopyAccounts` demonstrates that callers must partition copy inputs before
selecting a shard manually.

The larger checked-in fixture under `integration/fixture` compiles generated
same-package and separate-package layouts and is exercised against five local
PostgreSQL databases by `just verify`.
