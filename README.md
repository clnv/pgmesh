# pgmesh

[![Test](https://github.com/clnv/pgmesh/actions/workflows/test.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/test.yml)
[![Lint](https://github.com/clnv/pgmesh/actions/workflows/lint.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/lint.yml)
[![Generated](https://github.com/clnv/pgmesh/actions/workflows/generated.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/generated.yml)
[![Vulnerability](https://github.com/clnv/pgmesh/actions/workflows/vulnerability.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/vulnerability.yml)
[![Integration](https://github.com/clnv/pgmesh/actions/workflows/integration.yml/badge.svg)](https://github.com/clnv/pgmesh/actions/workflows/integration.yml)

`pgmesh` is a standalone sqlc companion for PostgreSQL and pgx/v5. Its Go
package is also named `pgmesh`. A process plugin generates read/write-separated
query wrappers, while the runtime routes those wrappers across virtual shards,
primary databases, read replicas, and synchronous dual-write mirrors for staged
shard expansion.

## Documentation

- [Purpose and rationale](docs/purpose-and-rationale.md)
- [Quickstart](docs/quickstart.md)
- [How-to guides](docs/how-to/README.md) for adding queries, sharding, replicas,
  shard-expansion dual writes, transactions, OpenTelemetry, generation layouts,
  and troubleshooting
- [Runnable examples](examples)

## Installation

Add the runtime module to an application with:

```bash
go get github.com/clnv/pgmesh
```

Import it using the package name `pgmesh`:

```go
import "github.com/clnv/pgmesh"
```

## Generated layers

For each sqlc package, the plugin generates:

- `ReadQuerier` and `ReadQueries`, containing only read queries.
- `WriteQuerier` and `WriteQueries`, containing only writes and mirror fan-out.
- `StoreQuerier` and `StoreQueries`, combining both views for a primary.
- `NewStoreNode`, pairing a read-only view with the primary-capable view.
- `ShardResolver[SK]` and `ShardedQueries[SK]` when at least one query declares
  a shard route.

This split makes `Shard.Read()` return a type that cannot execute writes.
`Shard.Write()` returns `StoreQueries`, because primary reads and transactional
reads must use the primary as well.

## Examples

The module includes executable examples in several forms:

- [`examples`](examples) is a progressive suite covering a plain single
  database, primary/replica splitting, multi-database sharding, synchronous
  mirrors, and shard-pinned transactions.
- [`example_test.go`](example_test.go) demonstrates direct `NewBuilder` usage,
  round-robin replicas, primary fallback, deterministic physical-shard
  enumeration, declarative `CreateMesh`, and synchronous mirrors.
- [`integration/fixture/example_test.go`](integration/fixture/example_test.go)
  exercises the actual generated `ShardedQueries` API, including replica reads,
  forced-primary reads, primary writes, and mirror fan-out.
- [`examples/sqlc`](examples/sqlc) is the shared schema, annotated query set,
  and `sqlc.yaml` process-plugin configuration.

Run the executable documentation examples with:

```bash
go test -run '^Example' ./...
```

The complete `just verify` workflow also runs every standalone program in
`examples` against the local PostgreSQL topology.

## Query annotations

Every query must put its kind immediately after the sqlc name annotation:

```sql
-- name: ListUsers :many
-- kind: read
SELECT * FROM users;

-- name: CreateUser :one
-- kind: write
INSERT INTO users (id, tenant_id) VALUES ($1, $2) RETURNING *;
```

A single-shard query may put a named shard route immediately after its kind:

```sql
-- name: GetConversation :one
-- kind: read
-- shard: p2p(user_id, peer_id)
SELECT * FROM conversations
WHERE user_id = $1 AND peer_id = $2;
```

The route name becomes an exported resolver method, and operands refer to SQL
parameter names. The example generates:

```go
type ShardResolver[SK any] interface {
    P2P(userID int64, peerID int64) SK
}
```

The application implements this interface, so normalization, hashing domains,
and composite shard-key construction remain application decisions. The plugin
resolves the operands to either scalar Go parameters or fields on sqlc's params
struct, independent of `query_parameter_limit`.

Metadata must appear in this order: `name`, `kind`, optional `shard`, then
ordinary documentation. Missing, malformed, misplaced, or type-conflicting
metadata fails generation. Queries without `shard` remain available through
node-level wrappers and are omitted from `ShardedQueries`.

Automatic routing is deliberately unavailable for `:copyfrom` and `:batch*`.
Partition those inputs by shard and invoke node-level wrappers. The plugin does
not implement scatter-gather or cross-shard result merging.

## sqlc configuration

Build the process plugin and register it in `sqlc.yaml`:

```bash
go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
```

```yaml
version: "2"
plugins:
  - name: "pgmesh"
    process:
      cmd: "path/to/sqlc-gen-store"

sql:
  - engine: "postgresql"
    schema: "schema.sql"
    queries: "queries.sql"
    gen:
      go:
        package: "db"
        out: "db"
        sql_package: "pgx/v5"
        emit_interface: true
        query_parameter_limit: 1
        emit_params_struct_pointers: true
        emit_result_struct_pointers: true
        emit_pointers_for_null_types: true
    codegen:
      - plugin: "pgmesh"
        out: "db"
        options:
          package: "db"
          output_file_name: "zz_generated_store.go"
          type: "StoreQueries"
          constructor: "NewStoreQueries"
          sql_package: "pgx/v5"
          query_parameter_limit: 1
          emit_params_struct_pointers: true
          emit_result_struct_pointers: true
          emit_pointers_for_null_types: true
```

The plugin supports the sqlc commands `:one`, `:many`, `:exec`, `:execrows`,
`:execresult`, `:copyfrom`, `:batchexec`, `:batchone`, and `:batchmany` at the
node layer. It accepts sqlc-compatible rename, override, pointer, package,
constructor, and output naming options. `sql_package` must be `pgx/v5` and
`skip_with_tx` is rejected. `runtime_import_path` can override the default
`github.com/clnv/pgmesh` import for forks.

When the wrapper is generated into a package separate from sqlc's Go output,
set `internal_import_path` and, optionally, `internal_import_alias`. The plugin
then qualifies sqlc-generated models, params structs, batch result types,
enums, constructors, and interfaces through that import. The checked-in
fixture generates and compiles both same-package and separate-package layouts.

## Building a topology

Open each DSN in a node factory and use the generated node constructor:

```go
func createNode(
    ctx context.Context,
    dsn string,
) (pgmesh.Node[*db.ReadQueries, *db.StoreQueries], error) {
    pool, err := pgxpool.New(ctx, dsn)
    if err != nil {
        return pgmesh.Node[*db.ReadQueries, *db.StoreQueries]{}, err
    }
    return db.NewStoreNode(pool), nil
}

mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
    *db.ReadQueries,
    *db.StoreQueries,
    ShardKey,
]{
    ReplicaSets: []pgmesh.ReplicaSetSpec{
        {
            Name:     "shard-a",
            Primary:  pgmesh.Connection{DSN: primaryDSN},
            Replicas: []pgmesh.Connection{{DSN: replicaDSN}},
        },
    },
    Shards: pgmesh.Shards{
        NumVShards: 1024,
        Mappings: []pgmesh.VShardMapping{
            {
                VShards:        pgmesh.VShardRange(0, 1024),
                MainReplicaSet: "shard-a",
            },
        },
    },
    CreateNode:  createNode,
    ShardHasher: shardHasher,
})
```

Topology construction validates every virtual shard, replica-set reference,
mirror reference, name, DSN, factory, and hasher. `Mesh.Shard` also rejects a
hasher result outside the configured virtual-shard range. `AllShards` returns
physical shards deterministically in first-vshard order.

Mirror order is part of the topology: mappings for the same main replica set
must list the same mirrors in the same order, because the first non-ignored
mirror error is returned.

Direct construction is available through `NewNode`, `NewReplicaSet`, and
`NewBuilder` when topology does not come from DSNs.

## Routed queries and consistency

Construct the generated facade with the mesh and application resolver:

```go
queries := db.NewShardedQueries(mesh, resolver)

// Read replica by default; primary fallback when no replicas exist.
user, err := queries.GetUser(ctx, arg)

// Strong read from the selected shard's primary.
user, err = queries.GetUser(ctx, arg, db.ReadFromPrimary())

// Reads and writes in a transaction use its selected primary.
user, err = queries.GetUser(ctx, arg, db.WithTx(tx))
```

Writes always execute against the selected primary. A transaction-bound
wrapper deliberately drops mirrors, avoiding cross-database transactions.

## OpenTelemetry

Generated routed queries emit OpenTelemetry spans, operation counts, and
duration histograms. They use the global providers by default, or explicit
providers can be supplied through `Options.TracerProvider` and
`Options.MeterProvider` (or their `Builder` equivalents). The query context is
propagated to the selected database wrapper, allowing pgx instrumentation to
create child spans and metric exemplars to link back to traces.

See [Enable OpenTelemetry](docs/how-to/enable-opentelemetry.md) for setup and
the metric and attribute contract.

## Write mirrors for shard expansion

`VShardMapping.MirrorReplicaSets` is primarily a staged resharding mechanism.
Keep the old database as `MainReplicaSet`, add the future database as a mirror,
and dual-write live changes while historical rows are backfilled and verified.
After reconciliation, switch `MainReplicaSet` to the new database. The old
database can temporarily become the new database's mirror to preserve a
rollback window.

Generated mirrored write wrappers:

1. Execute the primary and return immediately on primary failure.
2. Execute mirrors sequentially after primary success.
3. Always retain result values returned by the primary.
4. Ignore `sql.ErrNoRows` from mirrors, useful for idempotent migrations.
5. Return the first other mirror error by default.

The old and new writes are ordered but not atomic: mirror failure does not roll
back a successful primary write. pgmesh also does not backfill or reconcile
data. Transaction-bound calls, `:batch*` result objects, direct pgx writes, and
older application versions bypass mirror fan-out; `:copyfrom` writes are
mirrored normally. Cover those paths with an outbox, CDC, or explicit replay
before cutover.

Avoid `ignore_mirror_error: true` during migration unless another durable
repair path captures failures. Follow the full
[old-to-new cutover guide](docs/how-to/add-write-mirrors.md) before switching
traffic.

## Development and verification

Development commands use [`just`](https://github.com/casey/just). Run `just`
without arguments to list the available recipes. Recipes are organized under
`just/` by responsibility:

- `generation.just` generates the integration fixture and example package.
- `testing.just` runs tests, vet, and golangci-lint.
- `integration.just` manages the local PostgreSQL topology and smoke tests.
- `toolings.just` installs pinned sqlc and golangci-lint binaries under `bin/`.

The main verification commands are:

| Command | Purpose |
| --- | --- |
| `just test` | Run all unit and example tests. |
| `just lint` | Run the pinned linter with `.golangci.yaml`. |
| `just verify-unit` | Regenerate checked-in code, then run tests, vet, and lint. |
| `just integration` | Start PostgreSQL, run integration and example smoke tests, then clean up. |
| `just verify` | Run both the non-Docker and Docker-backed verification suites. |

The checked-in integration fixture is generated by sqlc v1.31.1 and compiled as
part of the module tests. Table-driven unit tests cover topology validation,
hashing, virtual-shard ranges, annotation parsing, route conflicts, sqlc command
shapes, and generated option combinations.

Separate GitHub Actions workflows check formatting and module-file drift, build
every package, run tests with the race detector and coverage, lint, scan known
vulnerabilities, verify generated code, and execute the full Docker-backed
suite. Failed integration runs retain Docker logs for seven days; test runs
retain the coverage profile for seven days.

### Local PostgreSQL integration

`integration/docker-compose.yaml` starts five isolated PostgreSQL 18 databases:

| Endpoint | Default port | Purpose |
| --- | ---: | --- |
| `shard0-primary` | 25432 | Primary for virtual shard 0 |
| `shard0-replica0` | 25433 | First read endpoint for shard 0 |
| `shard0-replica1` | 25434 | Second read endpoint for shard 0 |
| `shard1-primary` | 25435 | Primary and read fallback for shard 1 |
| `shard0-mirror` | 25436 | Synchronous write mirror for shard 0 |

The replica containers are intentionally independent databases rather than a
streaming-replication cluster. Tests seed distinct marker rows into each one so
they can prove exactly which endpoint handled each generated read. The suite
then validates round-robin replica reads, primary fallback, forced-primary
reads, virtual-shard write routing, synchronous mirrors, real PostgreSQL mirror
errors, transaction pinning, mirror suppression in transactions, and manually
partitioned `COPY FROM` fan-out.

Run only the Docker-backed suite, including automatic startup, health waits,
race detection, and cleanup:

```bash
just integration
```

Run the complete local validation—generation, table-driven unit tests, vet,
lint, and the five-database integration suite—with:

```bash
just verify
```

For debugging, the lifecycle can be controlled separately:

```bash
just integration-up
just integration-test
just integration-down
```

Ports can be changed with `PGMESH_SHARD0_PRIMARY_PORT`,
`PGMESH_SHARD0_REPLICA0_PORT`, `PGMESH_SHARD0_REPLICA1_PORT`,
`PGMESH_SHARD1_PRIMARY_PORT`, and `PGMESH_SHARD0_MIRROR_PORT`.
The test also accepts full DSN overrides using the corresponding `_DSN`
variables.

Equivalent isolated non-Docker commands are:

```bash
just generate-fixture
go test ./...
go vet ./...
just lint
```
