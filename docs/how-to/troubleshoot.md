# Troubleshoot generation and routing

## Generation errors

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Missing required kind annotation | The query has no `kind` comment | Add `-- kind: read` or `-- kind: write` immediately after `-- name` |
| First comment must be kind | Ordinary documentation appears before `kind` | Move documentation after `kind` and optional `shard` |
| Invalid or misplaced shard annotation | `shard` is malformed or appears later | Use `-- shard: route(operand, ...)` directly after `kind` |
| Unknown shard operand | The route names a result column or nonexistent parameter | Name an input parameter recognized by sqlc |
| Conflicting route types | The same resolver method is inferred with incompatible operand types | Align the SQL parameter types or use different route names |
| Generated code does not compile | sqlc and plugin options differ | Align pointer, rename, override, package, and parameter-limit options |

Regenerate with the pinned repository toolchain:

```bash
just generate-example
go test ./...
```

## Topology construction errors

`CreateMesh` validates the topology before returning it:

- every replica-set name must be unique and non-empty;
- every primary and replica DSN must be non-empty;
- every mapping must reference known replica sets;
- every virtual shard must be mapped exactly once;
- mirror lists for one main replica set must be consistent;
- the node factory and shard hasher must be present.

Use `errors.Is` with the exported errors in
[`errors.go`](../../errors.go) when startup diagnostics need classification.

## A read cannot find a recent write

Default routed reads use configured replicas. PostgreSQL replication may not
have applied the write yet. Retry according to application policy or use:

```go
value, err := queries.GetAccount(ctx, arg, db.ReadFromPrimary())
```

pgmesh does not monitor replication lag.

## A write returns an error but the primary changed

A synchronous mirror may have failed after the primary succeeded. Generated
methods preserve the primary result but return the first non-ignored mirror
error. Treat retries as potentially duplicating the primary operation and make
mirrored writes idempotent where possible.

## A transaction reaches the wrong database

The transaction was probably opened from a pool that does not match the query's
resolved physical shard. Resolve with the same `ShardResolver`, call
`mesh.Shard`, and begin the transaction from that shard's retained primary
pool. pgmesh cannot validate the origin of a `pgx.Tx`.

## A batch or copy method is absent from `ShardedQueries`

This is intentional. pgmesh does not automatically partition `:copyfrom` or
`:batch*` inputs. Group them by shard, select each shard explicitly, and call
the node-level writer.

## Local integration failures

Run the topology lifecycle in separate steps to inspect it:

```bash
just integration-up
just integration-test
just examples-smoke
just integration-down
```

The default ports and their `PGMESH_*` overrides are documented in the
[root README](../../README.md#local-postgresql-integration). Ensure Docker is
available and no other process owns those ports.
