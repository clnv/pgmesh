//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/clnv/pgmesh/integration/fixture/separate/internal"
	"github.com/clnv/pgmesh/integration/fixture/separate/store"
	"github.com/clnv/pgmesh/integration/internal/testdb"
)

func TestSeparatePackageStoreAgainstPostgres(t *testing.T) {
	if !testdb.Enabled() {
		t.Skipf("set %s=1 and start integration/docker-compose.yaml", testdb.IntegrationEnv)
	}

	dsn, err := testdb.PrimaryEndpoint().DSN()
	require.NoError(t, err)
	pool, err := testdb.OpenPool(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

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
