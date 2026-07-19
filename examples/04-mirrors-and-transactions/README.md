# Shards, replicas, mirrors, and transactions

This topology demonstrates the dual-write phase of a physical-shard expansion:
the current shard remains authoritative while each normal generated write is
replayed synchronously to its future database after primary success. In a real
migration, keep this topology active while backfilling and reconciling data,
then switch the virtual-shard mapping to make the new database authoritative.

A transaction must be opened from the selected shard's retained primary pool
and passed with `WithTx`; generated transaction wrappers deliberately suppress
mirror fan-out. Transactional writes therefore need a separate outbox, CDC, or
replay path before a shard cutover.

See the [shard-expansion guide](../../docs/how-to/add-write-mirrors.md) for the
complete old-to-new rollout and rollback sequence.

Required variables are `ADV_SHARD0_PRIMARY_DSN`, `ADV_SHARD0_REPLICA_DSN`,
`ADV_SHARD0_MIRROR_DSN`, `ADV_SHARD1_PRIMARY_DSN`,
`ADV_SHARD1_REPLICA_DSN`, and `ADV_SHARD1_MIRROR_DSN`.
