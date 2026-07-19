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

const numVShards = 128

type config struct {
	shard0Primary string
	shard0Replica string
	shard1Primary string
	shard1Replica string
}

type poolRegistry struct {
	byDSN map[string]*pgxpool.Pool
}

type (
	accountNode = pgmesh.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]
	accountMesh = pgmesh.Mesh[*exampledb.ReadQueries, *exampledb.StoreQueries, uint64]
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
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	pools := newPoolRegistry()
	defer pools.close()

	mesh, err := createMesh(ctx, cfg, pools)
	if err != nil {
		return err
	}
	queries := exampledb.NewShardedQueries(mesh, tenantResolver{})
	return runRoutedQueries(ctx, queries)
}

func loadConfig() (config, error) {
	values, err := requiredEnvironment(
		"SHARD0_PRIMARY_DSN",
		"SHARD0_REPLICA_DSN",
		"SHARD1_PRIMARY_DSN",
		"SHARD1_REPLICA_DSN",
	)
	if err != nil {
		return config{}, err
	}
	return config{
		shard0Primary: values[0],
		shard0Replica: values[1],
		shard1Primary: values[2],
		shard1Replica: values[3],
	}, nil
}

func newPoolRegistry() *poolRegistry {
	return &poolRegistry{byDSN: make(map[string]*pgxpool.Pool)}
}

func (r *poolRegistry) close() {
	for _, pool := range r.byDSN {
		pool.Close()
	}
}

func (r *poolRegistry) createNode(ctx context.Context, dsn string) (
	accountNode,
	error,
) {
	if pool, ok := r.byDSN[dsn]; ok {
		return exampledb.NewStoreNode(pool), nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return accountNode{}, fmt.Errorf("open node: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return accountNode{}, fmt.Errorf("ping node: %w", err)
	}
	r.byDSN[dsn] = pool
	return exampledb.NewStoreNode(pool), nil
}

func createMesh(
	ctx context.Context,
	cfg config,
	pools *poolRegistry,
) (*accountMesh, error) {
	mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
		*exampledb.ReadQueries,
		*exampledb.StoreQueries,
		uint64,
	]{
		ReplicaSets: []pgmesh.ReplicaSetSpec{
			{
				Name:     "shard-0",
				Primary:  pgmesh.Connection{DSN: cfg.shard0Primary},
				Replicas: []pgmesh.Connection{{DSN: cfg.shard0Replica}},
			},
			{
				Name:     "shard-1",
				Primary:  pgmesh.Connection{DSN: cfg.shard1Primary},
				Replicas: []pgmesh.Connection{{DSN: cfg.shard1Replica}},
			},
		},
		Shards: pgmesh.Shards{
			NumVShards: numVShards,
			Mappings: []pgmesh.VShardMapping{
				{
					VShards:           pgmesh.VShardRange(0, 64),
					MainReplicaSet:    "shard-0",
					MirrorReplicaSets: nil,
				},
				{
					VShards:           pgmesh.VShardRange(64, numVShards),
					MainReplicaSet:    "shard-1",
					MirrorReplicaSets: nil,
				},
			},
		},
		CreateNode:  pools.createNode,
		ShardHasher: pgmesh.ModularShardHashFor[uint64](numVShards),
	})
	if err != nil {
		return nil, fmt.Errorf("create mesh: %w", err)
	}
	return mesh, nil
}

func runRoutedQueries(ctx context.Context, queries *exampledb.ShardedQueries[uint64]) error {
	account, err := queries.UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID:          3001,
		TenantID:    100,
		DisplayName: "shard one",
	})
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

func requiredEnvironment(names ...string) ([]string, error) {
	values := make([]string, len(names))
	for index, name := range names {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		values[index] = value
	}
	return values, nil
}
