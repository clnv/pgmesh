package fixture

import (
	"context"
	"fmt"

	"github.com/clnv/pgmesh"
)

func ExampleStore() {
	log := &callLog{}
	queries, err := NewStore(context.Background(), Sharded(ShardedConfig[uint64]{
		ReplicaSets: []ShardDatabaseConfig{
			{
				Name:     "main",
				Primary:  &fakeDB{name: "primary", log: log},
				Replicas: []DBTX{&fakeDB{name: "replica", log: log}},
			},
			{Name: "mirror", Primary: &fakeDB{name: "mirror", log: log}},
		},
		Shards: pgmesh.Shards{
			NumVShards: 1,
			Mappings: []pgmesh.VShardMapping{{
				VShards:           []uint64{0},
				MainReplicaSet:    "main",
				MirrorReplicaSets: []string{"mirror"},
			}},
		},
		ShardHasher: pgmesh.ConstantShardHashFor[uint64](0),
		Resolver:    tenantResolver{},
	}))
	if err != nil {
		panic(err)
	}

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
