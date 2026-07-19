# Add read replicas

pgmesh treats replication as deployment infrastructure. Configure and monitor
PostgreSQL replication first, then expose replica DSNs to the topology.

## Declarative topology

Add one or more `Replicas` to a physical replica-set specification:

```go
{
    Name:    "shard-0",
    Primary: pgmesh.Connection{DSN: shard0PrimaryDSN},
    Replicas: []pgmesh.Connection{
        {DSN: shard0Replica0DSN},
        {DSN: shard0Replica1DSN},
    },
}
```

`CreateMesh` calls the same node factory for primaries and replicas. The
generated `NewStoreNode` provides a read-only view and a primary-capable view;
replica sets expose only the read-only view for configured replicas.

## Direct construction

Without `CreateMesh`, construct a replica set explicitly:

```go
replicaSet := pgmesh.NewReplicaSet(
    "accounts",
    db.NewStoreNode(primaryPool),
    []pgmesh.Node[*db.ReadQueries, *db.StoreQueries]{
        db.NewStoreNode(replica0Pool),
        db.NewStoreNode(replica1Pool),
    },
)
```

## Routing behavior

- `replicaSet.Read()` chooses configured replicas round-robin.
- `replicaSet.Write()` always uses the primary and also supports strong reads.
- With no replicas, `Read()` falls back to the primary.
- Generated sharded read methods use `Read()` by default.
- `db.ReadFromPrimary()` makes a generated sharded read use `Write()`.

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
runnable direct-construction example.
