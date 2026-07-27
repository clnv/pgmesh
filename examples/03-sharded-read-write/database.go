package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type poolRegistry struct {
	byDSN map[string]*pgxpool.Pool
}

func newPoolRegistry() *poolRegistry {
	return &poolRegistry{byDSN: make(map[string]*pgxpool.Pool)}
}

func (r *poolRegistry) close() {
	for _, pool := range r.byDSN {
		pool.Close()
	}
}

func (r *poolRegistry) open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if pool, ok := r.byDSN[dsn]; ok {
		return pool, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	r.byDSN[dsn] = pool
	return pool, nil
}
