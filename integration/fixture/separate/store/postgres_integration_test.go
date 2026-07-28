//go:build integration

package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/clnv/pgmesh/integration/fixture/separate/internal"
	"github.com/clnv/pgmesh/integration/fixture/separate/store"
)

func TestSeparatePackageStoreAgainstPostgres(t *testing.T) {
	if os.Getenv("PGMESH_INTEGRATION") == "" {
		t.Skip("set PGMESH_INTEGRATION=1 and start integration/docker-compose.yaml")
	}

	dsn := os.Getenv("PGMESH_SHARD0_PRIMARY_DSN")
	if dsn == "" {
		port := os.Getenv("PGMESH_SHARD0_PRIMARY_PORT")
		if port == "" {
			port = "25432"
		}
		dsn = fmt.Sprintf("postgres://pgmesh:pgmesh@127.0.0.1:%s/pgmesh?sslmode=disable", port)
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	pingContext, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	pingErr := pool.Ping(pingContext)
	cancel()
	require.NoError(t, pingErr)

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	queries, err := store.NewStore(
		t.Context(),
		store.Singleton(tx, store.WithDatabaseName("separate-package")),
	)
	require.NoError(t, err)

	created, err := queries.Users().CreateUser(
		t.Context(),
		&db.CreateUserParams{ID: 99001, TenantID: 99002, Name: "separate"},
	)
	require.NoError(t, err)
	require.NotNil(t, created)

	got, err := queries.Users().GetUser(
		t.Context(),
		&db.GetUserParams{TenantID: created.TenantID, ID: created.ID},
	)
	require.NoError(t, err)
	assert.Equal(t, created, got)
}
