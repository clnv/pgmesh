package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	exampledb "github.com/clnv/pgmesh/examples/internal/db"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("SINGLE_DATABASE_DSN")
	if dsn == "" {
		return errors.New("SINGLE_DATABASE_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if pingErr := pool.Ping(ctx); pingErr != nil {
		return fmt.Errorf("ping database: %w", pingErr)
	}

	queries := exampledb.NewStoreQueries(pool)
	account, err := queries.UpsertAccount(ctx, &exampledb.UpsertAccountParams{
		ID:          1001,
		TenantID:    42,
		DisplayName: "single database",
	})
	if err != nil {
		return fmt.Errorf("upsert account: %w", err)
	}
	loaded, err := queries.GetAccount(ctx, &exampledb.GetAccountParams{
		TenantID: account.TenantID,
		ID:       account.ID,
	})
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	fmt.Printf("account %d: %s\n", loaded.ID, loaded.DisplayName)
	return nil
}
