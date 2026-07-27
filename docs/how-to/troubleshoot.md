# Troubleshoot generation and routing

## Generation errors

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Missing required kind annotation | The query has no `kind` comment | Add `-- kind: read` or `-- kind: write` immediately after `-- name` |
| First comment must be kind | Ordinary documentation appears before `kind` | Move documentation after `kind` and optional `shard` |
| Invalid or misplaced shard annotation | `shard` is malformed or appears later | Use `-- shard: route(operand, ...)` directly after `kind` |
| Unknown shard operand | The route names a result column or nonexistent parameter | Name an input parameter recognized by sqlc |
| Conflicting route types | The same resolver method is inferred with incompatible operand types | Align the SQL parameter types or use different route names |
| A sharded store contains an unsharded query | One generated store mixes routing models | Add shard metadata or move the model and queries to another generated package |
| Generated code does not compile | sqlc and plugin options differ | Align pointer, rename, override, package, and parameter-limit options |

Regenerate with the pinned repository toolchain:

```bash
just --justfile examples/justfile generate
go test ./...
```

## Topology construction errors

`NewStore` validates the opaque topology before returning it:

- every replica-set name must be unique and non-empty;
- every configured primary and replica database must be present;
- every mapping must reference known replica sets;
- every virtual shard must be mapped exactly once;
- mirror lists for one main replica set must be consistent;
- the shard resolver and hasher must be present for sharded configurations.

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
mirrored writes idempotent where possible. Do not switch a virtual-shard mapping
to the new database until the failure has been repaired and the databases have
been reconciled; follow the
[shard-expansion cutover guide](add-write-mirrors.md).

## A transaction reaches the wrong database

The transaction was probably opened from a pool that does not match the query's
resolved physical shard. Use the same resolver, hasher, and mapping held in the
store configuration to choose the retained primary pool. pgmesh cannot
validate the origin of a `pgx.Tx`.

## A batch or copy method cannot be generated with sharding

This is intentional. pgmesh does not automatically partition `:copyfrom` or
`:batch*` inputs. Put manually partitioned operations in a separate unsharded
generated store.

## Local integration failures

Run the topology lifecycle in separate steps to inspect it:

```bash
just integration-up
just integration-test
just examples-smoke
just integration-down
```

The default ports and their `PGMESH_*` overrides are documented in
[Development and verification](../development.md#local-postgresql-integration).
Ensure Docker is available and no other process owns those ports.
