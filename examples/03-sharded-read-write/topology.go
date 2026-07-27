package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/clnv/pgmesh"
	"github.com/clnv/pgmesh/examples/internal/one"
	"github.com/clnv/pgmesh/examples/internal/sharded"
)

const numVShards = 128

type (
	accountStore  = sharded.Store
	settingsStore = one.Store
)

type tenantResolver struct{}

func (tenantResolver) Tenant(tenantID int64) uint64 {
	if tenantID < 0 {
		panic("tenant ID must not be negative")
	}
	return uint64(tenantID)
}

func newAccountQueries(
	ctx context.Context,
	cfg config,
	pools *poolRegistry,
) (accountStore, error) {
	shard0Primary, err := pools.open(ctx, cfg.shard0Primary)
	if err != nil {
		return nil, err
	}
	shard0Replica, err := pools.open(ctx, cfg.shard0Replica)
	if err != nil {
		return nil, err
	}
	shard1Primary, err := pools.open(ctx, cfg.shard1Primary)
	if err != nil {
		return nil, err
	}
	shard1Replica, err := pools.open(ctx, cfg.shard1Replica)
	if err != nil {
		return nil, err
	}
	store, err := sharded.NewStore(ctx, sharded.Sharded(sharded.ShardedConfig[uint64]{
		ReplicaSets: []sharded.ShardDatabaseConfig{
			{
				Name:     "shard-0",
				Primary:  shard0Primary,
				Replicas: []sharded.DBTX{shard0Replica},
			},
			{
				Name:     "shard-1",
				Primary:  shard1Primary,
				Replicas: []sharded.DBTX{shard1Replica},
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
		ShardHasher:    pgmesh.ModularShardHashFor[uint64](numVShards),
		Resolver:       tenantResolver{},
		TracerProvider: nil,
		MeterProvider:  nil,
		Logger: slog.New(slog.NewTextHandler(
			os.Stderr,
			&slog.HandlerOptions{
				AddSource:   false,
				Level:       slog.LevelDebug,
				ReplaceAttr: nil,
			},
		)),
	}))
	if err != nil {
		return nil, fmt.Errorf("create account store: %w", err)
	}
	return store, nil
}

func newSettingsStore(
	ctx context.Context,
	dsn string,
	pools *poolRegistry,
) (settingsStore, error) {
	pool, err := pools.open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return one.NewStore(ctx, one.Database(one.DatabaseConfig{
		Name:           "settings",
		Primary:        pool,
		Replicas:       nil,
		Mirrors:        nil,
		TracerProvider: nil,
		MeterProvider:  nil,
		Logger:         nil,
	}))
}
