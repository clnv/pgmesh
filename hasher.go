package pgmesh

// ShardHasher maps an application shard key to a virtual shard index.
type ShardHasher[SK any] interface {
	// Hash returns the virtual shard index for key.
	Hash(SK) uint64
}

type constantHasher[SK any] struct {
	vshard uint64
}

func (h constantHasher[SK]) Hash(SK) uint64 {
	return h.vshard
}

// ConstantShardHashFor returns a hasher that always selects vshard.
func ConstantShardHashFor[SK any](vshard uint64) ShardHasher[SK] {
	return constantHasher[SK]{vshard: vshard}
}

// IntShardKey is the set of integer types supported by ModularShardHashFor.
type IntShardKey interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type modularHasher[SK IntShardKey] struct {
	numVShards uint64
}

func (h modularHasher[SK]) Hash(key SK) uint64 {
	if key < 0 {
		magnitudeRemainder := (uint64(-(key + 1)) + 1) % h.numVShards
		if magnitudeRemainder == 0 {
			return 0
		}
		return h.numVShards - magnitudeRemainder
	}
	return uint64(key) % h.numVShards
}

// ModularShardHashFor returns a hasher that maps integer keys modulo numVShards.
// Signed keys use Euclidean modulo, so negative values map into the same
// [0, numVShards) range without overflowing at the minimum integer value.
// Named integer types are supported. It panics if numVShards is zero.
func ModularShardHashFor[SK IntShardKey](numVShards uint64) ShardHasher[SK] {
	if numVShards == 0 {
		panic("pgmesh: numVShards must not be zero")
	}
	return modularHasher[SK]{numVShards: numVShards}
}
