# Primary and read replicas

`ReplicaSet.Read()` returns only the generated read wrapper and balances calls
across replicas. `ReplicaSet.Write()` returns the primary-capable wrapper for
writes and strong reads.

```bash
RW_PRIMARY_DSN='postgres://user:pass@primary/accounts?sslmode=disable' \
RW_REPLICA_DSN='postgres://user:pass@replica/accounts?sslmode=disable' \
  go run ./examples/02-read-write-split
```
