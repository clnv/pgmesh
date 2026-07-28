# Primary and read replicas

The generated `Store` is constructed with one primary and one replica.
Ordinary reads are balanced across replicas, writes use the primary, and
`ReadFromPrimary()` requests a strong read. Those routing details do not
change the generated `AccountsReader` and `AccountsWriter` interfaces used by
the application.

```bash
RW_PRIMARY_DSN='postgres://user:pass@primary/accounts?sslmode=disable' \
RW_REPLICA_DSN='postgres://user:pass@replica/accounts?sslmode=disable' \
  go run ./examples/02-read-write-split
```
