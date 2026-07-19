# Add sharding

This guide turns generated query wrappers into a routed `ShardedQueries`
facade. It assumes relevant SQL queries already have `shard` annotations; see
[Add a query](add-a-query.md).

## 1. Choose a stable logical key

Implement the generated resolver interface. For an annotation such as
`shard: tenant(tenant_id)`, the generated interface includes `Tenant`:

```go
type tenantResolver struct{}

func (tenantResolver) Tenant(tenantID int64) uint64 {
    if tenantID < 0 {
        panic("tenant ID must not be negative")
    }
    return uint64(tenantID)
}
```

Keep the resolver behavior stable after data is written. If a route combines
multiple operands, normalize and combine them deterministically.

## 2. Choose the virtual-shard count and hash

The hash must return an index in `[0, NumVShards)`. Integer keys can use the
built-in modular hasher:

```go
const numVShards = 128

hasher := pgmesh.ModularShardHashFor[uint64](numVShards)
```

Use a custom `pgmesh.ShardHasher` when the key is not integer-like or when an
existing system already defines the hash. Changing the hash changes placement
and therefore requires a data migration.

## 3. Describe physical replica sets

Each replica set represents one physical shard. Start with one primary per
set; replicas and mirrors can be added separately.

```go
replicaSets := []pgmesh.ReplicaSetSpec{
    {
        Name:    "shard-0",
        Primary: pgmesh.Connection{DSN: shard0DSN},
    },
    {
        Name:    "shard-1",
        Primary: pgmesh.Connection{DSN: shard1DSN},
    },
}
```

Names must be unique and non-empty. DSNs must be non-empty.

## 4. Map every virtual shard exactly once

```go
mappings := []pgmesh.VShardMapping{
    {
        VShards:        pgmesh.VShardRange(0, 64),
        MainReplicaSet: "shard-0",
    },
    {
        VShards:        pgmesh.VShardRange(64, 128),
        MainReplicaSet: "shard-1",
    },
}
```

`VShardRange(from, to)` is half-open. Every index from zero through
`NumVShards - 1` must occur once. Missing, duplicate, and out-of-range entries
are rejected when the mesh is created.

Changing this mapping tells pgmesh where requests should go; it does not move
existing rows. Move or copy data before switching production traffic.

## 5. Create nodes and the mesh

The node factory owns connection construction. Retain the pools so the
application can close them during shutdown and begin transactions on a
specific primary later.

```go
pools := make([]*pgxpool.Pool, 0, len(replicaSets))

createNode := func(ctx context.Context, dsn string) (
    pgmesh.Node[*db.ReadQueries, *db.StoreQueries],
    error,
) {
    pool, err := pgxpool.New(ctx, dsn)
    if err != nil {
        return pgmesh.Node[*db.ReadQueries, *db.StoreQueries]{}, err
    }
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return pgmesh.Node[*db.ReadQueries, *db.StoreQueries]{}, err
    }
    pools = append(pools, pool)
    return db.NewStoreNode(pool), nil
}

mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
    *db.ReadQueries,
    *db.StoreQueries,
    uint64,
]{
    ReplicaSets: replicaSets,
    Shards: pgmesh.Shards{
        NumVShards: numVShards,
        Mappings:   mappings,
    },
    CreateNode:  createNode,
    ShardHasher: hasher,
})
if err != nil {
    for _, pool := range pools {
        pool.Close()
    }
    return err
}
```

`Mesh` is immutable after construction and safe to share across requests.

## 6. Construct the routed facade

```go
resolver := tenantResolver{}
queries := db.NewShardedQueries(mesh, resolver)
```

Normal routed reads use a replica when one is configured. Writes always use
the selected physical shard's primary:

```go
account, err := queries.UpsertAccount(ctx, &db.UpsertAccountParams{
    ID:          accountID,
    TenantID:    tenantID,
    DisplayName: "Ada",
})
```

Force a routed read to the primary when current data is required:

```go
account, err := queries.GetAccount(
    ctx,
    &db.GetAccountParams{TenantID: tenantID, ID: accountID},
    db.ReadFromPrimary(),
)
```

## Operational checklist

- Apply compatible schema to every physical database before routing traffic.
- Confirm the resolver and hasher match the placement used by existing data.
- Move data before changing a virtual-shard mapping.
- Keep old and new application versions compatible during a topology rollout.
- Close every pool created by the node factory during shutdown.

The complete runnable version is
[`examples/03-sharded-read-write`](../../examples/03-sharded-read-write).
