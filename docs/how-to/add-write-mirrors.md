# Add synchronous write mirrors

A write mirror receives the same generated write after the primary succeeds.
This is synchronous application-level fan-out, not PostgreSQL replication.

## 1. Register mirror databases

Mirrors are ordinary replica-set specifications. A mirror needs a primary but
usually no read replicas:

```go
ReplicaSets: []pgmesh.ReplicaSetSpec{
    {
        Name:    "shard-0",
        Primary: pgmesh.Connection{DSN: shard0PrimaryDSN},
    },
    {
        Name:    "mirror-0",
        Primary: pgmesh.Connection{DSN: mirror0DSN},
    },
},
```

## 2. Attach mirrors to a mapping

```go
Shards: pgmesh.Shards{
    NumVShards: 1,
    Mappings: []pgmesh.VShardMapping{
        {
            VShards:           []uint64{0},
            MainReplicaSet:    "shard-0",
            MirrorReplicaSets: []string{"mirror-0"},
        },
    },
},
```

Mappings that reuse the same main replica set must list the same mirrors in the
same order. A replica set cannot mirror itself, and a mirror cannot be repeated
in one mapping.

## 3. Understand write behavior

For each generated write:

1. the primary runs first;
2. primary failure stops the operation before any mirror runs;
3. mirrors run sequentially in configured order;
4. the primary result is retained;
5. `sql.ErrNoRows` from a mirror is ignored;
6. the first other mirror error is returned and later mirrors do not run.

A returned mirror error does not mean the primary was rolled back. Design
retries and idempotency with that partial-success case in mind.

## Ignore mirror errors when appropriate

Set the plugin option below when mirrors are best-effort and callers should not
receive their errors:

```yaml
codegen:
  - plugin: "pgmesh"
    out: "internal/db"
    options:
      package: "db"
      ignore_mirror_error: true
      # Keep the remaining options aligned with gen.go.
```

The mirrors still execute; only their errors are discarded.

## Transactions

Generated `WithTx` wrappers deliberately omit mirrors because a `pgx.Tx`
belongs to one database connection. If a transaction must be reflected
elsewhere, use an outbox, change-data-capture pipeline, or another explicit
post-commit mechanism.

See [`examples/04-mirrors-and-transactions`](../../examples/04-mirrors-and-transactions)
for the complete topology.
