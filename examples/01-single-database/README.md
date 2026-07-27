# Single database

This is the baseline: construct the generated `Store` with a `DatabaseConfig`
containing one pgx pool. The query API is the same as in the replica and
sharded examples.

```bash
SINGLE_DATABASE_DSN='postgres://user:pass@localhost/accounts?sslmode=disable' \
  go run ./examples/01-single-database
```
