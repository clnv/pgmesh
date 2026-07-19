package fixture

import (
	"context"
	"fmt"

	"github.com/clnv/pgmesh"
)

func ExampleShardedQueries() {
	log := &callLog{}
	primary := NewStoreNode(&fakeDB{name: "primary", log: log})
	replica := NewStoreNode(&fakeDB{name: "replica", log: log})
	mirror := NewStoreNode(&fakeDB{name: "mirror", log: log})
	replicaSet := pgmesh.NewReplicaSet(
		"main",
		primary,
		[]pgmesh.Node[*ReadQueries, *StoreQueries]{replica},
	).WithWriteMirrors(mirror.Writer())
	mesh, err := pgmesh.NewBuilder[*ReadQueries, *StoreQueries, uint64](1).
		WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).
		Link(0, replicaSet).
		Build()
	if err != nil {
		panic(err)
	}
	queries := NewShardedQueries(mesh, tenantResolver{})

	ctx := context.Background()
	if _, err := queries.GetUser(ctx, &GetUserParams{TenantID: 10, ID: 20}); err != nil {
		panic(err)
	}
	if _, err := queries.GetUser(ctx, &GetUserParams{TenantID: 10, ID: 20}, ReadFromPrimary()); err != nil {
		panic(err)
	}
	if _, err := queries.CreateUser(ctx, &CreateUserParams{ID: 20, TenantID: 10, Name: "user"}); err != nil {
		panic(err)
	}

	fmt.Println(log.snapshot())

	// Output:
	// [replica primary primary mirror]
}
