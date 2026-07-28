//go:build integration

package commands_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/clnv/pgmesh/tests/generate/commands/internal"
	"github.com/clnv/pgmesh/tests/generate/commands/store"
	"github.com/clnv/pgmesh/tests/integration/testdb"
)

func TestGeneratedCommandShapesAgainstPostgres(t *testing.T) {
	if !testdb.Enabled() {
		t.Skipf("set %s=1 and start tests/integration/docker-compose.yaml", testdb.IntegrationEnv)
	}

	primaryPool := openCommandPool(t, "shard0-primary")
	mirrorPool := openCommandPool(t, "shard0-mirror")
	primaryTx, err := primaryPool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = primaryTx.Rollback(context.Background()) })
	mirrorTx, err := mirrorPool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mirrorTx.Rollback(context.Background()) })
	_, err = primaryTx.Exec(t.Context(), "TRUNCATE TABLE users")
	require.NoError(t, err)
	_, err = mirrorTx.Exec(t.Context(), "TRUNCATE TABLE users")
	require.NoError(t, err)

	queries, err := store.NewStore(
		t.Context(),
		store.Singleton(
			primaryTx,
			store.WithDatabaseName("commands"),
			store.WithWriteMirrors(mirrorTx),
		),
	)
	require.NoError(t, err)
	commands := queries.Commands()

	copyCount, err := commands.CopyCommandUsers(t.Context(), []*db.CopyCommandUsersParams{
		{ID: 700, TenantID: 70, Name: "copy-a"},
		{ID: 701, TenantID: 70, Name: "copy-b"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), copyCount)
	assertCommandUserName(t, primaryTx, 700, "copy-a")
	assertCommandUserName(t, mirrorTx, 700, "copy-a")
	assertCommandUserName(t, primaryTx, 701, "copy-b")
	assertCommandUserName(t, mirrorTx, 701, "copy-b")

	got, err := commands.GetCommandUser(t.Context(), 700)
	require.NoError(t, err)
	assert.Equal(t, "copy-a", got.Name)
	users, err := commands.ListCommandUsers(t.Context())
	require.NoError(t, err)
	require.Len(t, users, 2)

	tag, err := commands.TouchCommandUser(t.Context(), 700)
	require.NoError(t, err)
	assert.Equal(t, int64(1), tag.RowsAffected())
	require.NoError(t, commands.DeleteCommandUser(t.Context(), 701))
	assertCommandUserAbsent(t, primaryTx, 701)
	assertCommandUserAbsent(t, mirrorTx, 701)
	deleted, err := commands.DeleteCommandUsersByTenant(t.Context(), 70)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assertCommandUserAbsent(t, primaryTx, 700)
	assertCommandUserAbsent(t, mirrorTx, 700)

	batch := commands.BatchInsertCommandUsers(t.Context(), []*db.BatchInsertCommandUsersParams{
		{ID: 710, TenantID: 71, Name: "batch-a"},
		{ID: 711, TenantID: 72, Name: "batch-b"},
	})
	var batchErrors []error
	batch.Exec(func(_ int, err error) {
		batchErrors = append(batchErrors, err)
	})
	require.Len(t, batchErrors, 2)
	for _, batchErr := range batchErrors {
		require.NoError(t, batchErr)
	}
	assertCommandUserName(t, primaryTx, 710, "batch-a")
	assertCommandUserName(t, primaryTx, 711, "batch-b")
	assertCommandUserAbsent(t, mirrorTx, 710)
	assertCommandUserAbsent(t, mirrorTx, 711)

	batchGet := commands.BatchGetCommandUser(
		t.Context(),
		[]int64{710, 711},
		store.ReadFromPrimary(),
	)
	var batchGetNames []string
	batchGet.QueryRow(func(_ int, user *db.User, err error) {
		require.NoError(t, err)
		batchGetNames = append(batchGetNames, user.Name)
	})
	assert.Equal(t, []string{"batch-a", "batch-b"}, batchGetNames)

	batchList := commands.BatchListCommandUsersByTenant(
		t.Context(),
		[]int64{71, 72},
		store.ReadFromPrimary(),
	)
	var batchListNames []string
	batchList.Query(func(_ int, users []*db.User, err error) {
		require.NoError(t, err)
		require.Len(t, users, 1)
		batchListNames = append(batchListNames, users[0].Name)
	})
	assert.Equal(t, []string{"batch-a", "batch-b"}, batchListNames)
}

func openCommandPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	for _, endpoint := range testdb.DefaultEndpoints() {
		if endpoint.Name != name {
			continue
		}
		dsn, err := endpoint.DSN()
		require.NoError(t, err)
		pool, err := testdb.OpenPool(t.Context(), dsn)
		require.NoError(t, err)
		t.Cleanup(pool.Close)
		return pool
	}
	require.FailNowf(t, "integration endpoint is not configured", "endpoint %q", name)
	return nil
}

func assertCommandUserName(t *testing.T, tx pgx.Tx, id int64, want string) {
	t.Helper()
	var name string
	err := tx.QueryRow(t.Context(), "SELECT name FROM users WHERE id = $1", id).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, want, name)
}

func assertCommandUserAbsent(t *testing.T, tx pgx.Tx, id int64) {
	t.Helper()
	var ignored int64
	err := tx.QueryRow(t.Context(), "SELECT id FROM users WHERE id = $1", id).Scan(&ignored)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
