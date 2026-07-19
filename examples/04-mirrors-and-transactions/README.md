# Shards, replicas, mirrors, and transactions

This topology adds one synchronous write mirror per physical shard. A normal
write fans out after primary success. A transaction must be opened from the
selected shard's retained primary pool and passed with `WithTx`; generated
transaction wrappers deliberately suppress mirror fan-out.

Required variables are `ADV_SHARD0_PRIMARY_DSN`, `ADV_SHARD0_REPLICA_DSN`,
`ADV_SHARD0_MIRROR_DSN`, `ADV_SHARD1_PRIMARY_DSN`,
`ADV_SHARD1_REPLICA_DSN`, and `ADV_SHARD1_MIRROR_DSN`.
