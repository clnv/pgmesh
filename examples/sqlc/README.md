# sqlc process-plugin example

This directory is a minimal PostgreSQL/sqlc project showing the annotation
grammar and process-plugin configuration. Use the dedicated examples
`justfile` to build the local plugin and run the pinned sqlc version. From the
module root:

```bash
cd examples
just generate
```

The checked-in generated package at `../internal/db` contains node-level
read/write wrappers and a typed `ShardedQueries[SK]` facade. `ListAccounts` has
no shard annotation, so it is available only through the node-level wrappers.
`CopyAccounts` demonstrates that callers must partition copy inputs before
selecting a shard manually.

The larger checked-in fixture under `integration/fixture` compiles generated
same-package and separate-package layouts and is exercised against five local
PostgreSQL databases by `just verify`.
