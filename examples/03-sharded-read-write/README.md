# Sharding with read/write splitting

This example maps 128 virtual shards over two physical replica sets. Generated
routed methods derive the shard key through `tenantResolver`; reads use
replicas by default and writes always use the selected primary.

The sharded account store exposes two groups: `Accounts()` for commands and
lookups, and `Reports()` for the tenant-scoped account count. It also reads and
writes `application_settings` through a separate generated `Store` backed by
one database. The root-store configurations keep account routing sharded and
settings routing unsharded.

Required variables are `SHARD0_PRIMARY_DSN`, `SHARD0_REPLICA_DSN`,
`SHARD1_PRIMARY_DSN`, `SHARD1_REPLICA_DSN`, and `SETTINGS_DSN`.
