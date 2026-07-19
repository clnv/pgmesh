package pgmesh

import "errors"

var (
	ErrNoReplicaSets       = errors.New("pgmesh: at least one replica set is required")
	ErrEmptyReplicaSetName = errors.New("pgmesh: replica set name must not be empty")
	ErrDuplicateReplicaSet = errors.New("pgmesh: duplicate replica set")
	ErrEmptyDSN            = errors.New("pgmesh: connection DSN must not be empty")
	ErrNoVShards           = errors.New("pgmesh: at least one virtual shard is required")
	ErrDuplicateVShard     = errors.New("pgmesh: virtual shard is already linked")
	ErrMissingVShard       = errors.New("pgmesh: virtual shard is not linked")
	ErrVShardOutOfRange    = errors.New("pgmesh: virtual shard is out of range")
	ErrNoShardHasher       = errors.New("pgmesh: shard hasher is required")
	ErrNoNodeFactory       = errors.New("pgmesh: node factory is required")
	ErrUnknownReplicaSet   = errors.New("pgmesh: unknown replica set")
	ErrNilReplicaSet       = errors.New("pgmesh: replica set must not be nil")
	ErrMirrorConfiguration = errors.New("pgmesh: inconsistent mirror configuration")
)
