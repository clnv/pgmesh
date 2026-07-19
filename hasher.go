package sqlcstore

type ShardHasher[SK any] interface {
	Hash(SK) uint64
}

type constantHasher[SK any] struct {
	vshard uint64
}

func (h constantHasher[SK]) Hash(SK) uint64 {
	return h.vshard
}

func ConstantShardHashFor[SK any](vshard uint64) ShardHasher[SK] {
	return constantHasher[SK]{vshard: vshard}
}

type IntShardKey interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type modularHasher[SK IntShardKey] struct {
	numVShards uint64
}

func (h modularHasher[SK]) Hash(key SK) uint64 {
	return uint64(key) % h.numVShards
}

func ModularShardHashFor[SK IntShardKey](numVShards uint64) ShardHasher[SK] {
	if numVShards == 0 {
		panic("sqlcstore: numVShards must not be zero")
	}
	return modularHasher[SK]{numVShards: numVShards}
}
