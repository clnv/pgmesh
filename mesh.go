package pgmesh

import "fmt"

type virtualShard[R any, W Mirrorable[W]] struct {
	index      uint64
	replicaSet *ReplicaSet[R, W]
}

// Shard is a routed virtual shard linked to a physical replica set.
type Shard[R any, W Mirrorable[W]] struct {
	*ReplicaSet[R, W]

	vshardIndex uint64
}

func (s *Shard[R, W]) VShardIndex() uint64 {
	return s.vshardIndex
}

// Mesh routes logical shard keys through virtual shards to physical replica
// sets. Its topology is immutable after construction and safe for concurrent
// use.
type Mesh[R any, W Mirrorable[W], SK any] struct {
	vshards  []virtualShard[R, W]
	physical []*Shard[R, W]
	hasher   ShardHasher[SK]
}

func (m *Mesh[R, W, SK]) Shard(key SK) (*Shard[R, W], error) {
	index := m.hasher.Hash(key)
	if index >= uint64(len(m.vshards)) {
		return nil, fmt.Errorf("%w: got %d, valid range is [0,%d)", ErrVShardOutOfRange, index, len(m.vshards))
	}
	vshard := m.vshards[index]
	return &Shard[R, W]{vshardIndex: index, ReplicaSet: vshard.replicaSet}, nil
}

// AllShards returns one entry per physical replica set in first-vshard order.
func (m *Mesh[R, W, SK]) AllShards() []*Shard[R, W] {
	return append([]*Shard[R, W](nil), m.physical...)
}
