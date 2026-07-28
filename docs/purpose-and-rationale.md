# Purpose and rationale

## The problem

sqlc generates type-safe methods for SQL, but the generated API normally does
not express where each method may run. Once an application has primaries, read
replicas, or physical shards, routing becomes application code repeated around
otherwise safe query methods.

That creates several easy mistakes:

- a write reaches a read replica;
- a read that requires current data reaches a lagging replica;
- two methods derive the same logical shard differently;
- a new query bypasses mirror or transaction behavior;
- endpoint selection becomes entangled with business code.

pgmesh moves those decisions into generated wrappers and one immutable runtime
topology.

## How pgmesh divides the work

Annotated SQL is processed by both sqlc and the pgmesh process plugin. sqlc
continues to generate models and database methods. pgmesh generates a public
root `Store` plus the required query-group interfaces. Application query code
starts from that root:

```go
func loadAccount(ctx context.Context, store db.Store, arg *db.GetAccountParams) (*db.Account, error) {
    return store.Accounts().GetAccount(ctx, arg)
}
```

Every query has a `store: Accounts`-style annotation that groups related
methods behind `store.Accounts()`. The generator also
emits `AccountsReader`, `AccountsWriter`, and combined `Accounts` interfaces, so
packages can depend on the narrow capability they use without splitting one
database into artificial generated packages.

`NewStore(ctx, topology, options...)` chooses the internal implementation.
`Singleton` accepts one primary plus functional options for read replicas and
write mirrors. `Sharded` accepts the virtual-shard count, hasher, generated
`ShardResolver`, and functional options for replica sets and mappings. Both
topologies return the same `Store`.

| Capability | Configuration |
| --- | --- |
| Single database | `Singleton(pool)` |
| Read/write separation | Add `WithReadReplicas(...)` |
| Sharding | Use `Sharded(...)` when every query has a `shard` annotation |
| Write mirrors | Add `WithWriteMirrors(...)` or sharded mirror mappings |
| Transactions | Pass the same generated `WithTx(tx)` query option |

The generated read, write, one-database, replica-set, and sharded executors are
unexported. They enforce endpoint capabilities internally without becoming
part of the application API. A generated store cannot mix routed and
unrouted queries; unsharded models belong in a separate generated package and
still expose the same `Store` shape.

At runtime, a `Mesh` maps a logical key through a virtual shard to a physical
`ReplicaSet`. Reads use replica readers by default. Writes and explicit strong
reads use the primary writer. Configured write mirrors run synchronously after
the primary succeeds. Their main purpose is to dual-write from an old physical
shard to a future shard during a staged expansion or replacement.

This division keeps SQL ownership in sqlc, endpoint capabilities in generated
Go types, and deployment topology in application configuration.

## Why generated wrappers

The distinction between a reader and a writer is useful at both layers. A
replica is represented by a private read-only executor, so a generated write
cannot be sent to it. Each public group also has `Reader` and `Writer`
interfaces, letting application packages request only the operations they use.

Generation also lets routed method signatures remain aligned with sqlc options
such as parameter structs, pointers, renames, overrides, and result shapes.

## Why virtual shards

A logical key first maps to one of a fixed number of virtual shards. Virtual
shards then map to physical replica sets. This separates the stable hash space
used by application keys from the current database layout.

Changing a virtual-shard mapping is still an operational data-movement event.
pgmesh validates and applies the mapping; it does not copy rows or coordinate a
cutover.

## Why synchronous write mirrors

Adding physical shards usually needs an overlap period: the old database still
serves traffic while the new database is backfilled and verified. A write
mirror lets generated writes commit to the old primary first and then replay to
the future shard. After reconciliation, the application switches the
virtual-shard mapping and makes the new database authoritative.

This is intentionally a migration primitive rather than permanent replication.
The two writes are ordered but not atomic, historical rows still require a
backfill, and transaction-bound or batch write paths require a separate replay
mechanism. See [Expand shards with synchronous dual writes](how-to/add-write-mirrors.md)
for the complete cutover sequence and its safety checks.

## Why the application owns shard resolution

The generated `ShardResolver` names routes from SQL annotations, but the
application implements them. This keeps domain choices—normalization,
composite keys, tenant aliases, and hash compatibility—outside the generator.

The resolver produces a logical shard key. A `ShardHasher` maps that key into
the configured virtual-shard range. Both must remain stable for existing data.

## Deliberate non-goals

pgmesh is not:

- a PostgreSQL proxy or connection pool;
- a replication system or replication-lag monitor;
- a schema migration or data-rebalancing tool;
- a distributed transaction coordinator;
- a scatter-gather query engine;
- an automatic cross-shard batch or `COPY FROM` partitioner.

Applications own pool lifecycle, database credentials, replication, schema
rollout, shard movement, and consistency policy. pgmesh provides a small,
validated routing layer on top of those choices.

## When to use it

pgmesh fits applications that already want sqlc and pgx/v5, need explicit
read/write separation, and prefer routing inside the Go type system rather than
behind a transparent database proxy.

Start with `Singleton(pool)`. Replicas, mirrors, and sharding can
then be introduced by changing construction without changing SQL method
definitions or application query interfaces.
