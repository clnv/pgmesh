package pgmesh_test

import (
	"context"
	"fmt"

	"github.com/clnv/pgmesh"
)

type exampleReader struct {
	node string
}

type exampleWriter struct {
	node    string
	mirrors []*exampleWriter
}

func (q *exampleWriter) WithMirrors(mirrors ...*exampleWriter) *exampleWriter {
	return &exampleWriter{
		node:    q.node,
		mirrors: append(append([]*exampleWriter(nil), q.mirrors...), mirrors...),
	}
}

func (q *exampleWriter) Put(value string) []string {
	writes := []string{q.node + ":" + value}
	for _, mirror := range q.mirrors {
		writes = append(writes, mirror.node+":"+value)
	}
	return writes
}

func exampleNode(name string) pgmesh.Node[*exampleReader, *exampleWriter] {
	return pgmesh.NewNode(
		&exampleReader{node: name},
		&exampleWriter{node: name, mirrors: nil},
	)
}

func ExampleNewBuilder() {
	shard0 := pgmesh.NewReplicaSet(
		"shard-0",
		exampleNode("shard0-primary"),
		[]pgmesh.Node[*exampleReader, *exampleWriter]{
			exampleNode("shard0-replica0"),
			exampleNode("shard0-replica1"),
		},
	)
	shard1 := pgmesh.NewReplicaSet("shard-1", exampleNode("shard1-primary"), nil)

	mesh, err := pgmesh.NewBuilder[*exampleReader, *exampleWriter, uint64](2).
		WithHasher(pgmesh.ModularShardHashFor[uint64](2)).
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
	mesh, err := pgmesh.CreateMesh(
		context.Background(),
		4,
		func(_ context.Context, dsn string) (
			pgmesh.Node[*exampleReader, *exampleWriter],
			error,
		) {
			return exampleNode(dsn), nil
		},
		pgmesh.ModularShardHashFor[uint64](4),
		pgmesh.WithReplicaSet("east", "east-primary", "east-replica"),
		pgmesh.WithReplicaSet("west", "west-primary"),
		pgmesh.WithReplicaSet("archive", "archive-primary"),
		pgmesh.WithVShardMapping("east", []uint64{0, 2}, "archive"),
		pgmesh.WithVShardMapping("west", []uint64{1, 3}),
	)
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
