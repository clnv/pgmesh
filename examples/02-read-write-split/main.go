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

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	primary, err := openPool(ctx, "RW_PRIMARY_DSN")
	if err != nil {
		return err
	}
	defer primary.Close()
	replica, err := openPool(ctx, "RW_REPLICA_DSN")
	if err != nil {
		return err
	}
	defer replica.Close()

	replicaSet := sqlcstore.NewReplicaSet(
		"accounts",
		exampledb.NewStoreNode(primary),
		[]sqlcstore.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{
			exampledb.NewStoreNode(replica),
		},
	)
	account, err := replicaSet.Write().UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID:          2001,
		TenantID:    42,
		DisplayName: "primary write",
	})
	if err != nil {
		return fmt.Errorf("write primary: %w", err)
	}

	strong, err := replicaSet.Write().GetAccount(ctx, &exampledb.GetAccountParams{
		TenantID: account.TenantID,
		ID:       account.ID,
	})
	if err != nil {
		return fmt.Errorf("read primary: %w", err)
	}
	fmt.Printf("strong read: %s\n", strong.DisplayName)

	replicaCopy, err := replicaSet.Read().GetAccount(ctx, &exampledb.GetAccountParams{
		TenantID: account.TenantID,
		ID:       account.ID,
	})
	if err != nil {
		return fmt.Errorf("read replica (check replication and lag): %w", err)
	}
	fmt.Printf("replica read: %s\n", replicaCopy.DisplayName)
	return nil
}

func openPool(ctx context.Context, environment string) (*pgxpool.Pool, error) {
	dsn := os.Getenv(environment)
	if dsn == "" {
		return nil, fmt.Errorf("%s is required", environment)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", environment, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", environment, err)
	}
	return pool, nil
}
