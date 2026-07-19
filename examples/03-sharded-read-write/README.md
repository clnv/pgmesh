# Sharding with read/write splitting

This example maps 128 virtual shards over two physical replica sets. Generated
routed methods derive the shard key through `tenantResolver`; reads use
replicas by default and writes always use the selected primary.

Required variables are `SHARD0_PRIMARY_DSN`, `SHARD0_REPLICA_DSN`,
`SHARD1_PRIMARY_DSN`, and `SHARD1_REPLICA_DSN`.
