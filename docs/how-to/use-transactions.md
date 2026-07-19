# Use transactions

A transaction is owned by one PostgreSQL connection. Route first, begin the
transaction on that physical shard's primary pool, and then pass the transaction
to generated methods.

## Retain primary pools

When creating nodes, retain primary pools by replica-set name or DSN. pgmesh
does not expose or close the connections created by your node factory.

```go
primaryPools := map[string]*pgxpool.Pool{
    "shard-0": shard0PrimaryPool,
    "shard-1": shard1PrimaryPool,
}
```

## Resolve the target shard

Use the same resolver and mesh as the generated method:

```go
resolver := tenantResolver{}
key := resolver.Tenant(tenantID)
shard, err := mesh.Shard(key)
if err != nil {
    return err
}
```

The resolver produces the logical key; `mesh.Shard` applies the configured
hasher and virtual-shard mapping.

## Begin on the selected primary

```go
pool := primaryPools[shard.Name()]
tx, err := pool.Begin(ctx)
if err != nil {
    return err
}
defer func() {
    _ = tx.Rollback(ctx)
}()
```

## Pass the generated route option

```go
updated, err := queries.UpdateAccountName(
    ctx,
    &db.UpdateAccountNameParams{
        TenantID:    tenantID,
        ID:          accountID,
        DisplayName: "updated",
    },
    db.WithTx(tx),
)
if err != nil {
    return err
}

if err := tx.Commit(ctx); err != nil {
    return err
}
```

For reads, `WithTx(tx)` also pins execution to the transaction and therefore
the primary. It takes precedence over normal replica selection.

## Important constraints

- pgmesh does not verify that the supplied transaction belongs to the shard
  selected from the query arguments. Begin it from the matching primary pool.
- All queries in one transaction must resolve to the same physical shard.
- pgmesh does not coordinate cross-shard transactions.
- Transaction-bound generated wrappers do not fan writes out to mirrors.
- Always commit or roll back using normal pgx transaction handling.

The mirror exception is critical during physical-shard expansion: transactional
writes will not reach the future database through pgmesh. Capture and replay
them with an outbox, CDC, or another migration mechanism, and reconcile them
before cutover. See
[Expand shards with synchronous dual writes](add-write-mirrors.md).

The full runnable pattern is in
[`examples/04-mirrors-and-transactions`](../../examples/04-mirrors-and-transactions).
