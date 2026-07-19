package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/clnv/pgmesh"
	exampledb "github.com/clnv/pgmesh/examples/internal/db"
)

type tenantResolver struct{}

func (tenantResolver) Tenant(tenantID int64) uint64 {
	if tenantID < 0 {
		panic("tenant ID must not be negative")
	}
	return uint64(tenantID)
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dsns, err := requiredDSNs(
		"SHARD0_PRIMARY_DSN",
		"SHARD0_REPLICA_DSN",
		"SHARD1_PRIMARY_DSN",
		"SHARD1_REPLICA_DSN",
	)
	if err != nil {
		return err
	}
	var pools []*pgxpool.Pool
	defer func() {
		for _, pool := range pools {
			pool.Close()
		}
	}()

	mesh, err := sqlcstore.CreateMesh(ctx, &sqlcstore.Options[
		*exampledb.ReadQueries,
		*exampledb.StoreQueries,
		uint64,
	]{
		ReplicaSets: []sqlcstore.ReplicaSetSpec{
			{
				Name:     "shard-0",
				Primary:  sqlcstore.Connection{DSN: dsns["SHARD0_PRIMARY_DSN"]},
				Replicas: []sqlcstore.Connection{{DSN: dsns["SHARD0_REPLICA_DSN"]}},
			},
			{
				Name:     "shard-1",
				Primary:  sqlcstore.Connection{DSN: dsns["SHARD1_PRIMARY_DSN"]},
				Replicas: []sqlcstore.Connection{{DSN: dsns["SHARD1_REPLICA_DSN"]}},
			},
		},
		Shards: sqlcstore.Shards{
			NumVShards: 128,
			Mappings: []sqlcstore.VShardMapping{
				{VShards: sqlcstore.VShardRange(0, 64), MainReplicaSet: "shard-0", MirrorReplicaSets: nil},
				{VShards: sqlcstore.VShardRange(64, 128), MainReplicaSet: "shard-1", MirrorReplicaSets: nil},
			},
		},
		CreateNode: func(ctx context.Context, dsn string) (
			sqlcstore.Node[*exampledb.ReadQueries, *exampledb.StoreQueries],
			error,
		) {
			pool, poolErr := pgxpool.New(ctx, dsn)
			if poolErr != nil {
				return sqlcstore.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{}, poolErr
			}
			if pingErr := pool.Ping(ctx); pingErr != nil {
				pool.Close()
				return sqlcstore.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{}, pingErr
			}
			pools = append(pools, pool)
			return exampledb.NewStoreNode(pool), nil
		},
		ShardHasher: sqlcstore.ModularShardHashFor[uint64](128),
	})
	if err != nil {
		return fmt.Errorf("create mesh: %w", err)
	}
	queries := exampledb.NewShardedQueries(mesh, tenantResolver{})
	arg := &exampledb.UpsertAccountParams{ID: 3001, TenantID: 100, DisplayName: "shard one"}
	account, err := queries.UpsertAccount(ctx, arg)
	if err != nil {
		return fmt.Errorf("routed write: %w", err)
	}

	strong, err := queries.GetAccount(
		ctx,
		&exampledb.GetAccountParams{TenantID: account.TenantID, ID: account.ID},
		exampledb.ReadFromPrimary(),
	)
	if err != nil {
		return fmt.Errorf("routed primary read: %w", err)
	}
	fmt.Printf("tenant %d account: %s\n", strong.TenantID, strong.DisplayName)
	return nil
}

func requiredDSNs(names ...string) (map[string]string, error) {
	dsns := make(map[string]string, len(names))
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		dsns[name] = value
	}
	return dsns, nil
}
