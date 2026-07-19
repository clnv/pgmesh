package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/clnv/pgmesh"
	exampledb "github.com/clnv/pgmesh/examples/internal/db"
)

const (
	shard0Name       = "shard-0"
	shard1Name       = "shard-1"
	futureShard0Name = "future-shard-0"
	futureShard1Name = "future-shard-1"
	numVShards       = 2
)

type config struct {
	shard0Primary string
	shard0Replica string
	shard0Future  string
	shard1Primary string
	shard1Replica string
	shard1Future  string
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

	const tenantID int64 = 42
	const accountID int64 = 4001
	if dualWriteErr := dualWriteToFutureShard(ctx, queries, tenantID, accountID); dualWriteErr != nil {
		return dualWriteErr
	}
	updated, err := updateInTransaction(ctx, cfg, pools, mesh, queries, tenantID, accountID)
	if err != nil {
		return err
	}
	fmt.Printf("account %d: %s\n", updated.ID, updated.DisplayName)
	return nil
}

func loadConfig() (config, error) {
	values, err := requiredEnvironment(
		"ADV_SHARD0_PRIMARY_DSN",
		"ADV_SHARD0_REPLICA_DSN",
		"ADV_SHARD0_MIRROR_DSN",
		"ADV_SHARD1_PRIMARY_DSN",
		"ADV_SHARD1_REPLICA_DSN",
		"ADV_SHARD1_MIRROR_DSN",
	)
	if err != nil {
		return config{}, err
	}
	return config{
		shard0Primary: values[0],
		shard0Replica: values[1],
		shard0Future:  values[2],
		shard1Primary: values[3],
		shard1Replica: values[4],
		shard1Future:  values[5],
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

func (r *poolRegistry) pool(dsn string) (*pgxpool.Pool, error) {
	pool, ok := r.byDSN[dsn]
	if !ok {
		return nil, errors.New("pool for DSN is not registered")
	}
	return pool, nil
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
				Name:     shard0Name,
				Primary:  pgmesh.Connection{DSN: cfg.shard0Primary},
				Replicas: []pgmesh.Connection{{DSN: cfg.shard0Replica}},
			},
			{
				Name:     shard1Name,
				Primary:  pgmesh.Connection{DSN: cfg.shard1Primary},
				Replicas: []pgmesh.Connection{{DSN: cfg.shard1Replica}},
			},
			{Name: futureShard0Name, Primary: pgmesh.Connection{DSN: cfg.shard0Future}, Replicas: nil},
			{Name: futureShard1Name, Primary: pgmesh.Connection{DSN: cfg.shard1Future}, Replicas: nil},
		},
		Shards: pgmesh.Shards{
			NumVShards: numVShards,
			Mappings: []pgmesh.VShardMapping{
				{
					VShards:           []uint64{0},
					MainReplicaSet:    shard0Name,
					MirrorReplicaSets: []string{futureShard0Name},
				},
				{
					VShards:           []uint64{1},
					MainReplicaSet:    shard1Name,
					MirrorReplicaSets: []string{futureShard1Name},
				},
			},
		},
		CreateNode:     pools.createNode,
		ShardHasher:    pgmesh.ModularShardHashFor[uint64](numVShards),
		TracerProvider: nil,
		MeterProvider:  nil,
		Logger:         nil,
	})
	if err != nil {
		return nil, fmt.Errorf("create mesh: %w", err)
	}
	return mesh, nil
}

func dualWriteToFutureShard(
	ctx context.Context,
	queries *exampledb.ShardedQueries[uint64],
	tenantID int64,
	accountID int64,
) error {
	_, err := queries.UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID:          accountID,
		TenantID:    tenantID,
		DisplayName: "mirrored write",
	})
	if err != nil {
		return fmt.Errorf("dual-write account: %w", err)
	}
	return nil
}

func updateInTransaction(
	ctx context.Context,
	cfg config,
	pools *poolRegistry,
	mesh *accountMesh,
	queries *exampledb.ShardedQueries[uint64],
	tenantID int64,
	accountID int64,
) (*exampledb.Account, error) {
	shard, err := mesh.Shard(tenantResolver{}.Tenant(tenantID))
	if err != nil {
		return nil, fmt.Errorf("select transaction shard: %w", err)
	}
	dsn, err := cfg.primaryDSN(shard.Name())
	if err != nil {
		return nil, err
	}
	pool, err := pools.pool(dsn)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
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
		&exampledb.UpdateAccountNameParams{
			TenantID:    tenantID,
			ID:          accountID,
			DisplayName: "transactional update",
		},
		exampledb.WithTx(tx),
	)
	if err != nil {
		return nil, fmt.Errorf("transactional update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return updated, nil
}

func (c config) primaryDSN(shardName string) (string, error) {
	switch shardName {
	case shard0Name:
		return c.shard0Primary, nil
	case shard1Name:
		return c.shard1Primary, nil
	default:
		return "", fmt.Errorf("unknown primary shard %q", shardName)
	}
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
