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

type databasePools struct {
	primary *pgxpool.Pool
	replica *pgxpool.Pool
}

type accountsReplicaSet = pgmesh.ReplicaSet[*exampledb.ReadQueries, *exampledb.StoreQueries]

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	pools, err := openDatabasePools(ctx)
	if err != nil {
		return err
	}
	defer pools.close()

	replicaSet := newAccountsReplicaSet(pools)
	account, err := writeAccount(ctx, replicaSet)
	if err != nil {
		return err
	}
	if err := printPrimaryRead(ctx, replicaSet, account); err != nil {
		return err
	}
	return printReplicaRead(ctx, replicaSet, account)
}

func openDatabasePools(ctx context.Context) (*databasePools, error) {
	primary, err := openPool(ctx, "RW_PRIMARY_DSN")
	if err != nil {
		return nil, err
	}
	replica, err := openPool(ctx, "RW_REPLICA_DSN")
	if err != nil {
		primary.Close()
		return nil, err
	}
	return &databasePools{primary: primary, replica: replica}, nil
}

func (p *databasePools) close() {
	p.replica.Close()
	p.primary.Close()
}

func newAccountsReplicaSet(
	pools *databasePools,
) *accountsReplicaSet {
	return pgmesh.NewReplicaSet(
		"accounts",
		exampledb.NewStoreNode(pools.primary),
		[]pgmesh.Node[*exampledb.ReadQueries, *exampledb.StoreQueries]{
			exampledb.NewStoreNode(pools.replica),
		},
	)
}

func writeAccount(
	ctx context.Context,
	replicaSet *accountsReplicaSet,
) (*exampledb.Account, error) {
	account, err := replicaSet.Write().UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID:          2001,
		TenantID:    42,
		DisplayName: "primary write",
	})
	if err != nil {
		return nil, fmt.Errorf("write primary: %w", err)
	}
	return account, nil
}

func printPrimaryRead(
	ctx context.Context,
	replicaSet *accountsReplicaSet,
	account *exampledb.Account,
) error {
	strong, err := replicaSet.Write().GetAccount(ctx, accountKey(account))
	if err != nil {
		return fmt.Errorf("read primary: %w", err)
	}
	fmt.Printf("strong read: %s\n", strong.DisplayName)
	return nil
}

func printReplicaRead(
	ctx context.Context,
	replicaSet *accountsReplicaSet,
	account *exampledb.Account,
) error {
	replicaCopy, err := replicaSet.Read().GetAccount(ctx, accountKey(account))
	if err != nil {
		return fmt.Errorf("read replica (check replication and lag): %w", err)
	}
	fmt.Printf("replica read: %s\n", replicaCopy.DisplayName)
	return nil
}

func accountKey(account *exampledb.Account) *exampledb.GetAccountParams {
	return &exampledb.GetAccountParams{TenantID: account.TenantID, ID: account.ID}
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
