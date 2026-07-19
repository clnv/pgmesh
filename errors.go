package sqlcstore

import "errors"

var (
	ErrNoReplicaSets       = errors.New("sqlcstore: at least one replica set is required")
	ErrEmptyReplicaSetName = errors.New("sqlcstore: replica set name must not be empty")
	ErrDuplicateReplicaSet = errors.New("sqlcstore: duplicate replica set")
	ErrEmptyDSN            = errors.New("sqlcstore: connection DSN must not be empty")
	ErrNoVShards           = errors.New("sqlcstore: at least one virtual shard is required")
	ErrDuplicateVShard     = errors.New("sqlcstore: virtual shard is already linked")
	ErrMissingVShard       = errors.New("sqlcstore: virtual shard is not linked")
	ErrVShardOutOfRange    = errors.New("sqlcstore: virtual shard is out of range")
	ErrNoShardHasher       = errors.New("sqlcstore: shard hasher is required")
	ErrNoNodeFactory       = errors.New("sqlcstore: node factory is required")
	ErrUnknownReplicaSet   = errors.New("sqlcstore: unknown replica set")
	ErrNilReplicaSet       = errors.New("sqlcstore: replica set must not be nil")
	ErrMirrorConfiguration = errors.New("sqlcstore: inconsistent mirror configuration")
)
