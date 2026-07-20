# Add a query

## 1. Choose the sqlc command

Write the query using a sqlc command supported by pgmesh:

- `:one`, `:many`, `:exec`, `:execrows`, and `:execresult`;
- `:copyfrom`;
- `:batchexec`, `:batchone`, and `:batchmany`.

## 2. Classify it

Put `kind: read` or `kind: write` immediately after the sqlc name annotation:

```sql
-- name: ListAccounts :many
-- kind: read
SELECT id, tenant_id, display_name
FROM accounts
ORDER BY id;
```

Use `read` for non-mutating operations. They can still be sent to the primary
with `ReadFromPrimary()` or `WithTx()`. Use `write` for inserts, updates,
deletes, DDL, and other operations that mutate database state.

The classification controls the generated Go surface. Read methods appear on
`ReadQueries` and `StoreQueries`; write methods appear on `WriteQueries` and
`StoreQueries`.

## 3. Add a shard route when needed

For a query that can be routed from its arguments, add `shard` immediately
after `kind`:

```sql
-- name: GetAccount :one
-- kind: read
-- shard: tenant(tenant_id)
-- GetAccount returns one account within a tenant.
SELECT id, tenant_id, display_name
FROM accounts
WHERE tenant_id = $1 AND id = $2;
```

`tenant` becomes a method on the generated `ShardResolver`:

```go
type ShardResolver[SK any] interface {
    Tenant(tenantID int64) SK
}
```

Route operands name SQL parameters, not result columns. They must resolve to
compatible Go types anywhere the same route is used.

The annotation order is strict:

1. `-- name: ...`
2. `-- kind: read|write`
3. optional `-- shard: route(operand, ...)`
4. optional ordinary documentation comments

## 4. Regenerate and compile

Use your project's generation command. In this repository:

```bash
just --justfile examples/justfile generate
go test ./...
```

For a downstream project that invokes sqlc directly:

```bash
sqlc generate
go test ./...
```

Generation fails when metadata is missing, out of order, malformed, or refers
to an unknown parameter. Treat that failure as part of the query review rather
than moving routing into handwritten code.

## 5. Call the right generated surface

An unsharded query is available on the node-level wrappers:

```go
accounts, err := shard.Read().ListAccounts(ctx)
```

A query with a shard annotation is also available on `ShardedQueries`, which
derives the key and chooses the endpoint:

```go
account, err := queries.GetAccount(ctx, &db.GetAccountParams{
    TenantID: tenantID,
    ID:       accountID,
})
```

For a read-your-write operation, add the generated option:

```go
account, err := queries.GetAccount(ctx, arg, db.ReadFromPrimary())
```

## Batch and copy queries

`:copyfrom` and `:batch*` methods are generated at the node level, but pgmesh
does not put them on `ShardedQueries`. Partition inputs by shard in application
code, select each shard with `mesh.Shard`, and invoke the corresponding
node-level writer. pgmesh does not perform scatter-gather or merge results.
