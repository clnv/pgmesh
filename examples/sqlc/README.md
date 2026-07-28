# sqlc process-plugin example

This directory is a minimal PostgreSQL/sqlc project showing the annotation
grammar and process-plugin configuration. Use the dedicated examples
`justfile` to build the local plugin and run the pinned sqlc version. From the
module root:

```bash
cd examples
just generate
```

The checked-in generated packages expose only their topology-independent
`Store` interfaces and config-driven constructors. `internal/sharded` contains
the account store with shard annotations; `internal/one` contains the settings
store with its own model and no shard routes. Both are called through the same
generated API shape even though their internal routing differs.

The checked-in cases under `tests/generate` compile same-package,
separate-package, and command-shape layouts and are exercised against five
local PostgreSQL databases by `just verify`.
