# Quickstart

This guide starts with one PostgreSQL database. It generates read/write-aware
wrappers and calls them directly; sharding is an optional next step.

## Prerequisites

You need Go, PostgreSQL, sqlc, and pgx/v5. This repository pins its development
sqlc version in [`just/toolings.just`](../just/toolings.just).

Add pgmesh and pgx to your application:

```bash
go get github.com/clnv/pgmesh
go get github.com/jackc/pgx/v5
```

Install the process plugin somewhere on `PATH`:

```bash
go install github.com/clnv/pgmesh/cmd/sqlc-gen-store@latest
```

When working from a pgmesh checkout instead, build the local plugin with:

```bash
go build -o bin/sqlc-gen-store ./cmd/sqlc-gen-store
```

## 1. Add a schema

Create `db/schema.sql`:

```sql
CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    display_name TEXT NOT NULL
);
```

Apply the schema to the database using your normal migration tool.

## 2. Add annotated queries

Create `db/queries.sql`:

```sql
-- name: GetAccount :one
-- kind: read
SELECT id, tenant_id, display_name
FROM accounts
WHERE id = $1;

-- name: UpsertAccount :one
-- kind: write
INSERT INTO accounts (id, tenant_id, display_name)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    display_name = EXCLUDED.display_name
RETURNING id, tenant_id, display_name;
```

Every query needs `kind: read` or `kind: write` immediately after the sqlc
`name` annotation.

## 3. Configure sqlc and pgmesh

Create `sqlc.yaml`. The plugin options that affect generated Go types should
match the corresponding sqlc Go generator options.

```yaml
version: "2"
plugins:
  - name: "pgmesh"
    process:
      cmd: "sqlc-gen-store"

sql:
  - engine: "postgresql"
    schema: "db/schema.sql"
    queries: "db/queries.sql"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
        emit_interface: true
        query_parameter_limit: 1
        emit_params_struct_pointers: true
        emit_result_struct_pointers: true
        emit_pointers_for_null_types: true
    codegen:
      - plugin: "pgmesh"
        out: "internal/db"
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

If `sqlc-gen-store` is not on `PATH`, set `process.cmd` to its absolute or
project-relative path.

## 4. Generate the package

```bash
sqlc generate
```

Alongside sqlc's output, pgmesh generates `ReadQueries`, `WriteQueries`,
`StoreQueries`, and `NewStoreNode`.

Commit generated files when your project checks them in. Regenerate them after
every schema, query, annotation, or relevant sqlc option change.

## 5. Use the generated store

Replace `example.com/app/internal/db` with your generated package path:

```go
package main

import (
    "context"
    "log"

    "github.com/jackc/pgx/v5/pgxpool"

    "example.com/app/internal/db"
)

func main() {
    ctx := context.Background()
    pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost/app?sslmode=disable")
    if err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    queries := db.NewStoreQueries(pool)
    account, err := queries.UpsertAccount(ctx, &db.UpsertAccountParams{
        ID:          1,
        TenantID:    42,
        DisplayName: "Ada",
    })
    if err != nil {
        log.Fatal(err)
    }

    loaded, err := queries.GetAccount(ctx, account.ID)
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("loaded %s", loaded.DisplayName)
}
```

At this stage there is no runtime topology: both methods use the same pool.
The generated wrapper still establishes the query classification needed for
replicas, sharding, or staged shard-expansion dual writes later.

## Next steps

- [Add a query](how-to/add-a-query.md)
- [Add read replicas](how-to/add-read-replicas.md)
- [Add sharding](how-to/add-sharding.md)
- [Expand shards with synchronous dual writes](how-to/add-write-mirrors.md)
- Explore the [runnable examples](../examples)
