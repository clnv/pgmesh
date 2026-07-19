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
continues to generate models and database methods. pgmesh generates capability
views over those methods:

- `ReadQueries` contains only queries annotated `kind: read`.
- `WriteQueries` contains only queries annotated `kind: write`.
- `StoreQueries` combines both views for a primary connection.
- `NewStoreNode` pairs the read-only and primary-capable views.
- `ShardedQueries` routes queries that have a `shard` annotation.

At runtime, a `Mesh` maps a logical key through a virtual shard to a physical
`ReplicaSet`. Reads use replica readers by default. Writes and explicit strong
reads use the primary writer. Configured write mirrors run synchronously after
the primary succeeds.

This division keeps SQL ownership in sqlc, endpoint capabilities in generated
Go types, and deployment topology in application configuration.

## Why generated wrappers

The distinction between a reader and a writer is useful at compile time. A
replica is exposed as `ReadQueries`, so a write method is not available on that
value. Runtime reflection or a generic proxy could choose an endpoint, but it
could not provide the same method-level capability boundary.

Generation also lets routed method signatures remain aligned with sqlc options
such as parameter structs, pointers, renames, overrides, and result shapes.

## Why virtual shards

A logical key first maps to one of a fixed number of virtual shards. Virtual
shards then map to physical replica sets. This separates the stable hash space
used by application keys from the current database layout.

Changing a virtual-shard mapping is still an operational data-movement event.
pgmesh validates and applies the mapping; it does not copy rows or coordinate a
cutover.

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

For a single database, the generated `StoreQueries` wrapper can be used without
a `Mesh`. Replica sets and sharding can then be introduced without changing the
SQL method definitions.
