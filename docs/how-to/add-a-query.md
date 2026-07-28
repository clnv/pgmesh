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
-- store: Accounts
SELECT id, tenant_id, display_name
FROM accounts
ORDER BY id;
```

Use `read` for non-mutating operations. They can still be sent to the primary
with `ReadFromPrimary()` or `WithTx()`. Use `write` for inserts, updates,
deletes, DDL, and other operations that mutate database state.

The classification controls internal endpoint selection. Both read and write
methods appear on their required public store group.

## 3. Add a shard route when needed

For a query that can be routed from its arguments, add `shard` immediately
after `kind`:

```sql
-- name: GetAccount :one
-- kind: read
-- shard: tenant(tenant_id)
-- store: Accounts
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

Route operands normally name SQL parameters. They must resolve to compatible Go
types anywhere the same route is used.

Some shard-local queries do not need the shard value in their SQL. In that
case, give the route a routing-only operand:

```sql
-- name: ListTenantAccounts :many
-- kind: read
-- shard: tenant(tenant_id)
-- store: Accounts
SELECT id, display_name
FROM accounts
ORDER BY id
LIMIT @limit OFFSET @offset;
```

Although `tenant_id` is not a SQL parameter here, it names a column on the
generated `Account` table model. The generator uses that model's `TenantID`
field name and Go type, then wraps the original sqlc argument instead of passing
the routing-only value to SQL:

```go
type ListTenantAccountsShardParams struct {
    Arg      *ListTenantAccountsParams
    TenantID int64
}

accounts, err := queries.Accounts().ListTenantAccounts(
    ctx,
    &db.ListTenantAccountsShardParams{
        Arg:      &db.ListTenantAccountsParams{Limit: 100},
        TenantID: tenantID,
    },
)
```

Generation fails if a routing-only operand does not name a field on the query's
generated table model. For joins and ambiguous sqlc metadata, insert and result
models plus tables named by the SQL's source relations take precedence over
models inferred only from SQL parameters; matching models within the same tier
must produce compatible field names and Go types.

## 4. Group every query

Every query must declare its generated sub-interface. Add `store` after the
optional `shard` annotation:

```sql
-- name: GetAccount :one
-- kind: read
-- shard: tenant(tenant_id)
-- store: Accounts
SELECT id, tenant_id, display_name
FROM accounts
WHERE tenant_id = $1 AND id = $2;
```

The value must be an exported Go identifier. pgmesh generates `Accounts`,
`AccountsReader`, and `AccountsWriter` interfaces, and exposes the group from
the root:

```go
type Store interface {
    Accounts() Accounts
}
```

Call the query through `queries.Accounts().GetAccount(...)`. Generation fails
when any query is missing its `store` annotation.

The annotation order is strict:

1. `-- name: ...`
2. `-- kind: read|write`
3. optional `-- shard: route(operand, ...)`
4. required `-- store: ExportedGroup`
5. optional ordinary documentation comments

## 5. Regenerate and compile

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

Generation fails when metadata is missing, out of order, malformed, or contains
a routing-only operand that does not align with a generated table field. Treat
that failure as part of the query review rather than moving routing into
handwritten code.

## 6. Call the generated Store

Business code always calls the same generated interface:

```go
account, err := queries.Accounts().GetAccount(ctx, &db.GetAccountParams{
    TenantID: tenantID,
    ID:       accountID,
})
```

For a read-your-write operation, add the generated option:

```go
account, err := queries.Accounts().GetAccount(ctx, arg, db.ReadFromPrimary())
```

## Batch and copy queries

`:copyfrom` and `:batch*` methods are supported by unsharded stores. They cannot
declare shard metadata. A generated store that contains any sharded query
requires shard metadata on every query, so batch or copy operations that need
manual partitioning belong in a separate generated store.
