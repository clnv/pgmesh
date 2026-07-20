# pgmesh

[![Test](https://github.com/clnv/pgmesh/actions/workflows/test.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/test.yml)
[![Lint](https://github.com/clnv/pgmesh/actions/workflows/lint.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/lint.yml)
[![Integration](https://github.com/clnv/pgmesh/actions/workflows/integration.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/integration.yml)

**Type-safe PostgreSQL query routing for sqlc and pgx/v5.**

## Why

sqlc generates type-safe queries, but it does not express where each query may
run. As an application adds read replicas or shards, routing logic can spread
through business code and make it easy to send a write to the wrong database.

pgmesh keeps that policy in generated Go types and one validated runtime
topology.

## What

pgmesh is a sqlc process plugin plus a small Go runtime. Together they provide:

- separate read, write, and primary-capable query APIs;
- replica reads with explicit primary reads when consistency requires them;
- logical-key routing through virtual shards to physical databases;
- shard-pinned transactions;
- synchronous write mirrors for staged shard expansion; and
- OpenTelemetry instrumentation and structured debug logging.

It is not a database proxy, connection pool, replication system, data migration
tool, or distributed transaction coordinator. Your application keeps control
of those concerns.

## How

Install the runtime and generator:

```bash
go get github.com/clnv/pgmesh
go install github.com/clnv/pgmesh/cmd/sqlc-gen-store@latest
```

Classify each sqlc query. Add a shard route only when the query should be
routed automatically:

```sql
-- name: GetAccount :one
-- kind: read
-- shard: tenant(tenant_id)
SELECT * FROM accounts WHERE tenant_id = $1 AND id = $2;

-- name: UpsertAccount :one
-- kind: write
INSERT INTO accounts (id, tenant_id, display_name) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name
RETURNING *;
```

Register the process plugin in `sqlc.yaml`, then generate both sqlc's queries
and pgmesh's wrappers:

```bash
sqlc generate
```

Start with a single database using the generated store:

```go
queries := db.NewStoreQueries(pool)
account, err := queries.GetAccount(ctx, &db.GetAccountParams{
    TenantID: tenantID,
    ID:       accountID,
})
```

When the deployment grows, create a `Mesh` and use the generated routed facade.
The SQL methods stay the same while pgmesh chooses the shard and read or write
endpoint.

Follow the [quickstart](docs/quickstart.md) for a complete working setup, or
explore the [progressive examples](examples) from one database through replicas,
sharding, write mirrors, and transactions.

## Documentation

- [Documentation index](docs/README.md)
- [Topology concepts and request-routing flow](docs/topology.md)
- [Purpose, design, and non-goals](docs/purpose-and-rationale.md)
- [How-to guides](docs/how-to/README.md)
- [Development and verification](docs/development.md)

## License

[MIT](LICENSE)
