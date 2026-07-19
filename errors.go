package pgmesh

import "errors"

var (
	// ErrNoReplicaSets indicates that a topology contains no replica sets.
	ErrNoReplicaSets = errors.New("pgmesh: at least one replica set is required")
	// ErrEmptyReplicaSetName indicates that a replica set has no name.
	ErrEmptyReplicaSetName = errors.New("pgmesh: replica set name must not be empty")
	// ErrDuplicateReplicaSet indicates that a topology reuses a replica set name.
	ErrDuplicateReplicaSet = errors.New("pgmesh: duplicate replica set")
	// ErrEmptyDSN indicates that a database connection has no DSN.
	ErrEmptyDSN = errors.New("pgmesh: connection DSN must not be empty")
	// ErrNoVShards indicates that a topology contains no virtual shards.
	ErrNoVShards = errors.New("pgmesh: at least one virtual shard is required")
	// ErrDuplicateVShard indicates that a virtual shard has already been linked.
	ErrDuplicateVShard = errors.New("pgmesh: virtual shard is already linked")
	// ErrMissingVShard indicates that a virtual shard has not been linked.
	ErrMissingVShard = errors.New("pgmesh: virtual shard is not linked")
	// ErrVShardOutOfRange indicates that a virtual shard index is outside the topology.
	ErrVShardOutOfRange = errors.New("pgmesh: virtual shard is out of range")
	// ErrNoShardHasher indicates that no shard-key hasher was configured.
	ErrNoShardHasher = errors.New("pgmesh: shard hasher is required")
	// ErrNoNodeFactory indicates that no database node factory was configured.
	ErrNoNodeFactory = errors.New("pgmesh: node factory is required")
	// ErrUnknownReplicaSet indicates that a shard mapping names an undefined replica set.
	ErrUnknownReplicaSet = errors.New("pgmesh: unknown replica set")
	// ErrNilReplicaSet indicates that a builder was given a nil replica set.
	ErrNilReplicaSet = errors.New("pgmesh: replica set must not be nil")
	// ErrMirrorConfiguration indicates that write-mirror mappings are inconsistent.
	ErrMirrorConfiguration = errors.New("pgmesh: inconsistent mirror configuration")
)
