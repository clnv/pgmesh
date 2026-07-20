# pgmesh documentation

The documentation is organized by what you are trying to learn or accomplish.

## Start here

- [Purpose and rationale](purpose-and-rationale.md) explains the problems pgmesh
  solves, its design choices, and what remains the application's responsibility.
- [Quickstart](quickstart.md) generates and runs a read/write-aware query package
  against one PostgreSQL database.
- [How-to guides](how-to/README.md) provide focused procedures for extending a
  working application.
- [Development and verification](development.md) covers the local toolchain,
  test commands, and Docker-backed PostgreSQL topology.

## How-to guides

- [Add a query](how-to/add-a-query.md)
- [Add sharding](how-to/add-sharding.md)
- [Add read replicas](how-to/add-read-replicas.md)
- [Expand shards with synchronous dual writes](how-to/add-write-mirrors.md)
- [Use transactions](how-to/use-transactions.md)
- [Configure and regenerate code](how-to/configure-generation.md)
- [Troubleshoot generation and routing](how-to/troubleshoot.md)

## Runnable examples

The [`examples`](../examples) directory progresses from a single database to
read replicas, virtual sharding, shard-expansion dual writes, and transactions.
The [`integration/fixture`](../integration/fixture) package is the larger generated
fixture used by both unit and Docker-backed integration tests.

For query annotations and supported commands, see [Add a query](how-to/add-a-query.md).
For plugin options and output layouts, see
[Configure and regenerate code](how-to/configure-generation.md).
