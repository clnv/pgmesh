# Add read replicas

pgmesh treats replication as deployment infrastructure. Configure and monitor
PostgreSQL replication first, then pass the replica pools with
`WithReadReplicas`.

## Configure the store

Application query code continues to use `db.Store`:

```go
store, err := db.NewStore(
    ctx,
    db.Singleton(
        primaryPool,
        db.WithDatabaseName("accounts"),
        db.WithReadReplicas(replica0Pool, replica1Pool),
    ),
)
```

## Routing behavior

- Ordinary reads choose configured replicas round-robin.
- Writes always use the primary.
- With no replicas, reads fall back to the primary.
- `db.ReadFromPrimary()` routes a read to the primary.

For example:

```go
eventual, err := queries.GetAccount(ctx, arg)
strong, err := queries.GetAccount(ctx, arg, db.ReadFromPrimary())
```

## Consistency considerations

pgmesh does not measure or wait for replication lag. A default read immediately
after a write may observe older data. Use `ReadFromPrimary()` for read-your-write
paths, or implement an application-level consistency policy around replication
positions.

If an unhealthy replica should be removed or replaced, rebuild the immutable
topology with the desired endpoint set. Endpoint health checking and failover
are outside pgmesh.

See [`examples/02-read-write-split`](../../examples/02-read-write-split) for a
runnable example.
