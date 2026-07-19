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

const (
	shard0Name = "shard-0"
	shard1Name = "shard-1"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dsns, err := requiredDSNs(
		"ADV_SHARD0_PRIMARY_DSN",
		"ADV_SHARD0_REPLICA_DSN",
		"ADV_SHARD0_MIRROR_DSN",
		"ADV_SHARD1_PRIMARY_DSN",
		"ADV_SHARD1_REPLICA_DSN",
		"ADV_SHARD1_MIRROR_DSN",
	)
	if err != nil {
		return err
	}
	poolsByDSN := make(map[string]*pgxpool.Pool, len(dsns))
	defer func() {
		for _, pool := range poolsByDSN {
			pool.Close()
		}
	}()
	factory := func(ctx context.Context, dsn string) (
		pgmesh.Node[*exampledb.ReadQueries, *exampledb.StoreQueries],
		error,
	) {
		if pool, ok := poolsByDSN[dsn]; ok {
			return exampledb.NewStoreNode(pool), nil
		}
		pool, poolErr := pgxpool.New(ctx, dsn)
		if poolErr != nil {
			return pgmesh.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{}, poolErr
		}
		if pingErr := pool.Ping(ctx); pingErr != nil {
			pool.Close()
			return pgmesh.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{}, pingErr
		}
		poolsByDSN[dsn] = pool
		return exampledb.NewStoreNode(pool), nil
	}

	mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
		*exampledb.ReadQueries,
		*exampledb.StoreQueries,
		uint64,
	]{
		ReplicaSets: []pgmesh.ReplicaSetSpec{
			{
				Name:     shard0Name,
				Primary:  pgmesh.Connection{DSN: dsns["ADV_SHARD0_PRIMARY_DSN"]},
				Replicas: []pgmesh.Connection{{DSN: dsns["ADV_SHARD0_REPLICA_DSN"]}},
			},
			{
				Name:     shard1Name,
				Primary:  pgmesh.Connection{DSN: dsns["ADV_SHARD1_PRIMARY_DSN"]},
				Replicas: []pgmesh.Connection{{DSN: dsns["ADV_SHARD1_REPLICA_DSN"]}},
			},
			{Name: "mirror-0", Primary: pgmesh.Connection{DSN: dsns["ADV_SHARD0_MIRROR_DSN"]}, Replicas: nil},
			{Name: "mirror-1", Primary: pgmesh.Connection{DSN: dsns["ADV_SHARD1_MIRROR_DSN"]}, Replicas: nil},
		},
		Shards: pgmesh.Shards{
			NumVShards: 2,
			Mappings: []pgmesh.VShardMapping{
				{VShards: []uint64{0}, MainReplicaSet: shard0Name, MirrorReplicaSets: []string{"mirror-0"}},
				{VShards: []uint64{1}, MainReplicaSet: shard1Name, MirrorReplicaSets: []string{"mirror-1"}},
			},
		},
		CreateNode:  factory,
		ShardHasher: pgmesh.ModularShardHashFor[uint64](2),
	})
	if err != nil {
		return fmt.Errorf("create mesh: %w", err)
	}
	queries := exampledb.NewShardedQueries(mesh, tenantResolver{})
	tenantID := int64(42)
	accountID := int64(4001)

	// This primary write is followed synchronously by the configured mirror.
	if _, writeErr := queries.UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID: accountID, TenantID: tenantID, DisplayName: "mirrored write",
	}); writeErr != nil {
		return fmt.Errorf("mirrored write: %w", writeErr)
	}

	// Retain primary pools when creating the mesh so transactions can begin on
	// the same physical shard selected by the generated route.
	shard, err := mesh.Shard(uint64(tenantID))
	if err != nil {
		return fmt.Errorf("select transaction shard: %w", err)
	}
	primaryDSNs := map[string]string{
		shard0Name: dsns["ADV_SHARD0_PRIMARY_DSN"],
		shard1Name: dsns["ADV_SHARD1_PRIMARY_DSN"],
	}
	tx, err := poolsByDSN[primaryDSNs[shard.Name()]].Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				log.Printf("rollback transaction: %v", rollbackErr)
			}
		}
	}()
	updated, err := queries.UpdateAccountName(
		ctx,
		&exampledb.UpdateAccountNameParams{TenantID: tenantID, ID: accountID, DisplayName: "transactional update"},
		exampledb.WithTx(tx),
	)
	if err != nil {
		return fmt.Errorf("transactional update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	fmt.Printf("account %d: %s\n", updated.ID, updated.DisplayName)
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
