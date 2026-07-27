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

The classification controls internal endpoint selection. Both read and write
methods appear on the public `Store` interface.

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

## 5. Call the generated Store

Business code always calls the same generated interface:

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

`:copyfrom` and `:batch*` methods are supported by unsharded stores. They cannot
declare shard metadata. A generated store that contains any sharded query
requires shard metadata on every query, so batch or copy operations that need
manual partitioning belong in a separate generated store.
