package sqlcstore_test

import (
	"context"
	"fmt"

	"github.com/clnv/pgmesh"
)

type exampleReadQueries struct {
	node string
}

type exampleStoreQueries struct {
	node    string
	mirrors []*exampleStoreQueries
}

func (q *exampleStoreQueries) WithMirrors(mirrors ...*exampleStoreQueries) *exampleStoreQueries {
	return &exampleStoreQueries{
		node:    q.node,
		mirrors: append(append([]*exampleStoreQueries(nil), q.mirrors...), mirrors...),
	}
}

func (q *exampleStoreQueries) Put(value string) []string {
	writes := []string{q.node + ":" + value}
	for _, mirror := range q.mirrors {
		writes = append(writes, mirror.node+":"+value)
	}
	return writes
}

func exampleNode(name string) sqlcstore.Node[*exampleReadQueries, *exampleStoreQueries] {
	return sqlcstore.NewNode(
		&exampleReadQueries{node: name},
		&exampleStoreQueries{node: name, mirrors: nil},
	)
}

func ExampleNewBuilder() {
	shard0 := sqlcstore.NewReplicaSet(
		"shard-0",
		exampleNode("shard0-primary"),
		[]sqlcstore.Node[*exampleReadQueries, *exampleStoreQueries]{
			exampleNode("shard0-replica0"),
			exampleNode("shard0-replica1"),
		},
	)
	shard1 := sqlcstore.NewReplicaSet("shard-1", exampleNode("shard1-primary"), nil)

	mesh, err := sqlcstore.NewBuilder[*exampleReadQueries, *exampleStoreQueries, uint64](2).
		WithHasher(sqlcstore.ModularShardHashFor[uint64](2)).
		Link(0, shard0).
		Link(1, shard1).
		Build()
	if err != nil {
		panic(err)
	}

	routed, err := mesh.Shard(2)
	if err != nil {
		panic(err)
	}
	fmt.Println(routed.Name(), routed.VShardIndex())
	fmt.Println(routed.Read().node)
	fmt.Println(routed.Read().node)
	fmt.Println(routed.Write().Put("message"))

	fallback, err := mesh.Shard(3)
	if err != nil {
		panic(err)
	}
	fmt.Println(fallback.Read().node)

	for _, shard := range mesh.AllShards() {
		fmt.Println(shard.Name())
	}

	// Output:
	// shard-0 0
	// shard0-replica0
	// shard0-replica1
	// [shard0-primary:message]
	// shard1-primary
	// shard-0
	// shard-1
}

func ExampleCreateMesh() {
	mesh, err := sqlcstore.CreateMesh(context.Background(), &sqlcstore.Options[
		*exampleReadQueries,
		*exampleStoreQueries,
		uint64,
	]{
		ReplicaSets: []sqlcstore.ReplicaSetSpec{
			{
				Name:     "east",
				Primary:  sqlcstore.Connection{DSN: "east-primary"},
				Replicas: []sqlcstore.Connection{{DSN: "east-replica"}},
			},
			{Name: "west", Primary: sqlcstore.Connection{DSN: "west-primary"}},
			{Name: "archive", Primary: sqlcstore.Connection{DSN: "archive-primary"}},
		},
		Shards: sqlcstore.Shards{
			NumVShards: 4,
			Mappings: []sqlcstore.VShardMapping{
				{
					VShards:           []uint64{0, 2},
					MainReplicaSet:    "east",
					MirrorReplicaSets: []string{"archive"},
				},
				{VShards: []uint64{1, 3}, MainReplicaSet: "west"},
			},
		},
		CreateNode: func(_ context.Context, dsn string) (
			sqlcstore.Node[*exampleReadQueries, *exampleStoreQueries],
			error,
		) {
			return exampleNode(dsn), nil
		},
		ShardHasher: sqlcstore.ModularShardHashFor[uint64](4),
	})
	if err != nil {
		panic(err)
	}

	routed, err := mesh.Shard(6)
	if err != nil {
		panic(err)
	}
	fmt.Println(routed.Name(), routed.VShardIndex())
	fmt.Println(routed.Read().node)
	fmt.Println(routed.Write().Put("event"))

	// Output:
	// east 2
	// east-replica
	// [east-primary:event archive-primary:event]
}
