# Single database

This is the baseline: create `StoreQueries` directly from one pgx pool and use
the generated methods. The pgmesh topology runtime is not involved.

```bash
SINGLE_DATABASE_DSN='postgres://user:pass@localhost/accounts?sslmode=disable' \
  go run ./examples/01-single-database
```
