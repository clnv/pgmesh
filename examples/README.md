# Progressive usage examples

These programs use the same sqlc-generated package in `internal/db` and build
from the simplest deployment to the full runtime topology.

| Example | Topology | Features |
| --- | --- | --- |
| [`01-single-database`](01-single-database) | One PostgreSQL database | Plain generated sqlc wrapper; no `Mesh`, replicas, or shard resolver |
| [`02-read-write-split`](02-read-write-split) | One primary and one or more replicas | Type-safe primary writes, replica reads, round-robin selection, strong reads |
| [`03-sharded-read-write`](03-sharded-read-write) | Two physical shards, each with a replica | Declarative topology, virtual shards, generated routed facade |
| [`04-mirrors-and-transactions`](04-mirrors-and-transactions) | Two sharded primary/replica sets plus future-shard mirrors | Staged shard-expansion dual writes, primary-pinned transactions, mirror suppression in transactions |

The source schema and annotated queries are in [`sqlc`](sqlc). Regenerate the
shared package with sqlc v1.31.1 from the module directory:

```bash
just generate-example
```

Every program reads DSNs from environment variables. Apply `sqlc/schema.sql`
to each database before running one. For example:

```bash
SINGLE_DATABASE_DSN='postgres://user:pass@localhost/accounts?sslmode=disable' \
  go run ./examples/01-single-database
```

Replica examples assume PostgreSQL replication is configured outside this
library. A write followed immediately by a default replica read can therefore
observe normal replication lag; use the generated `ReadFromPrimary()` option
when read-your-write consistency is required.

`just verify` starts the module's local PostgreSQL topology and runs all four
programs as smoke tests after the integration suite. For the read/write-only
smoke test, the same local endpoint is used as both primary and replica; the
dedicated integration tests separately prove selection across distinct read
endpoints.
