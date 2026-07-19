//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/clnv/pgmesh"
	"github.com/clnv/pgmesh/integration/fixture"
)

const integrationEnv = "PGMESH_INTEGRATION"

type tenantResolver struct{}

func (tenantResolver) Tenant(tenantID int64) uint64 {
	return uint64(tenantID)
}

type postgresHarness struct {
	mesh    *pgmesh.Mesh[*fixture.ReadQueries, *fixture.StoreQueries, uint64]
	queries *fixture.ShardedQueries[uint64]
	pools   map[string]*pgxpool.Pool
}

func newPostgresHarness(t *testing.T) *postgresHarness {
	t.Helper()
	if os.Getenv(integrationEnv) == "" {
		t.Skipf("set %s=1 and start integration/docker-compose.yaml", integrationEnv)
	}

	endpoints := []struct {
		name        string
		dsnEnv      string
		portEnv     string
		defaultPort int
	}{
		{name: "shard0-primary", dsnEnv: "PGMESH_SHARD0_PRIMARY_DSN", portEnv: "PGMESH_SHARD0_PRIMARY_PORT", defaultPort: 25432},
		{name: "shard0-replica0", dsnEnv: "PGMESH_SHARD0_REPLICA0_DSN", portEnv: "PGMESH_SHARD0_REPLICA0_PORT", defaultPort: 25433},
		{name: "shard0-replica1", dsnEnv: "PGMESH_SHARD0_REPLICA1_DSN", portEnv: "PGMESH_SHARD0_REPLICA1_PORT", defaultPort: 25434},
		{name: "shard1-primary", dsnEnv: "PGMESH_SHARD1_PRIMARY_DSN", portEnv: "PGMESH_SHARD1_PRIMARY_PORT", defaultPort: 25435},
		{name: "shard0-mirror", dsnEnv: "PGMESH_SHARD0_MIRROR_DSN", portEnv: "PGMESH_SHARD0_MIRROR_PORT", defaultPort: 25436},
	}
	dsns := make(map[string]string, len(endpoints))
	for _, endpoint := range endpoints {
		dsn, err := integrationDSN(os.Getenv(endpoint.dsnEnv), os.Getenv(endpoint.portEnv), endpoint.defaultPort)
		require.NoError(t, err, "resolve DSN for %s", endpoint.name)
		dsns[endpoint.name] = dsn
	}

	pools := make(map[string]*pgxpool.Pool, len(dsns))
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
	})
	byDSN := make(map[string]*pgxpool.Pool, len(dsns))
	for name, dsn := range dsns {
		pool, err := pgxpool.New(t.Context(), dsn)
		require.NoError(t, err, "create pool for %s", name)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		err = pool.Ping(ctx)
		cancel()
		require.NoError(t, err, "ping %s", name)
		pools[name] = pool
		byDSN[dsn] = pool
	}
	mesh, err := pgmesh.CreateMesh(t.Context(), &pgmesh.Options[
		*fixture.ReadQueries,
		*fixture.StoreQueries,
		uint64,
	]{
		ReplicaSets: []pgmesh.ReplicaSetSpec{
			{
				Name:    "shard0",
				Primary: pgmesh.Connection{DSN: dsns["shard0-primary"]},
				Replicas: []pgmesh.Connection{
					{DSN: dsns["shard0-replica0"]},
					{DSN: dsns["shard0-replica1"]},
				},
			},
			{
				Name:    "shard1",
				Primary: pgmesh.Connection{DSN: dsns["shard1-primary"]},
			},
			{
				Name:    "shard0-mirror",
				Primary: pgmesh.Connection{DSN: dsns["shard0-mirror"]},
			},
		},
		Shards: pgmesh.Shards{
			NumVShards: 2,
			Mappings: []pgmesh.VShardMapping{
				{VShards: []uint64{0}, MainReplicaSet: "shard0", MirrorReplicaSets: []string{"shard0-mirror"}},
				{VShards: []uint64{1}, MainReplicaSet: "shard1"},
			},
		},
		CreateNode: func(_ context.Context, dsn string) (pgmesh.Node[*fixture.ReadQueries, *fixture.StoreQueries], error) {
			pool, ok := byDSN[dsn]
			if !ok {
				return pgmesh.Node[*fixture.ReadQueries, *fixture.StoreQueries]{}, fmt.Errorf("unknown test DSN %q", dsn)
			}
			return fixture.NewStoreNode(pool), nil
		},
		ShardHasher: pgmesh.ModularShardHashFor[uint64](2),
	})
	require.NoError(t, err)

	return &postgresHarness{
		mesh:    mesh,
		queries: fixture.NewShardedQueries(mesh, tenantResolver{}),
		pools:   pools,
	}
}

func integrationDSN(dsnOverride, portOverride string, defaultPort int) (string, error) {
	if dsnOverride != "" {
		return dsnOverride, nil
	}
	port := defaultPort
	if portOverride != "" {
		parsed, err := strconv.Atoi(portOverride)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", fmt.Errorf("port override must be a valid TCP port: %q", portOverride)
		}
		port = parsed
	}
	return fmt.Sprintf("postgres://pgmesh:pgmesh@127.0.0.1:%d/pgmesh?sslmode=disable", port), nil
}

func TestIntegrationDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dsn         string
		port        string
		defaultPort int
		want        string
		wantErr     string
	}{
		{
			name:        "default port",
			defaultPort: 25432,
			want:        "postgres://pgmesh:pgmesh@127.0.0.1:25432/pgmesh?sslmode=disable",
		},
		{
			name:        "port override",
			port:        "35432",
			defaultPort: 25432,
			want:        "postgres://pgmesh:pgmesh@127.0.0.1:35432/pgmesh?sslmode=disable",
		},
		{name: "full DSN override", dsn: "postgres://custom", port: "invalid", defaultPort: 25432, want: "postgres://custom"},
		{name: "invalid port", port: "invalid", defaultPort: 25432, wantErr: "valid TCP port"},
		{name: "zero port", port: "0", defaultPort: 25432, wantErr: "valid TCP port"},
		{name: "port too large", port: "65536", defaultPort: 25432, wantErr: "valid TCP port"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := integrationDSN(test.dsn, test.port, test.defaultPort)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func (h *postgresHarness) reset(t *testing.T) {
	t.Helper()
	for name, pool := range h.pools {
		_, err := pool.Exec(t.Context(), "TRUNCATE TABLE users")
		require.NoError(t, err, "truncate %s", name)
	}
}

func (h *postgresHarness) insert(t *testing.T, database string, id, tenantID int64, name string) {
	t.Helper()
	_, err := h.pools[database].Exec(
		t.Context(),
		"INSERT INTO users (id, tenant_id, name) VALUES ($1, $2, $3)",
		id,
		tenantID,
		name,
	)
	require.NoError(t, err)
}

func (h *postgresHarness) userName(t *testing.T, database string, id, tenantID int64) string {
	t.Helper()
	var name string
	err := h.pools[database].QueryRow(
		t.Context(),
		"SELECT name FROM users WHERE id = $1 AND tenant_id = $2",
		id,
		tenantID,
	).Scan(&name)
	require.NoError(t, err, "read user from %s", database)
	return name
}

func (h *postgresHarness) assertUserAbsent(t *testing.T, database string, id, tenantID int64) {
	t.Helper()
	var ignored int64
	err := h.pools[database].QueryRow(
		t.Context(),
		"SELECT id FROM users WHERE id = $1 AND tenant_id = $2",
		id,
		tenantID,
	).Scan(&ignored)
	require.ErrorIs(t, err, pgx.ErrNoRows, "user unexpectedly exists in %s", database)
}

func TestPostgresTopologyIntegration(t *testing.T) {
	harness := newPostgresHarness(t)

	tests := []struct {
		name string
		run  func(*testing.T, *postgresHarness)
	}{
		{
			name: "round robin replicas and primary fallback",
			run: func(t *testing.T, h *postgresHarness) {
				h.insert(t, "shard0-primary", 100, 2, "primary")
				h.insert(t, "shard0-replica0", 100, 2, "replica0")
				h.insert(t, "shard0-replica1", 100, 2, "replica1")
				h.insert(t, "shard1-primary", 101, 3, "shard1-primary")

				first, err := h.queries.GetUser(t.Context(), &fixture.GetUserParams{TenantID: 2, ID: 100})
				require.NoError(t, err)
				second, err := h.queries.GetUser(t.Context(), &fixture.GetUserParams{TenantID: 2, ID: 100})
				require.NoError(t, err)
				strong, err := h.queries.GetUser(
					t.Context(),
					&fixture.GetUserParams{TenantID: 2, ID: 100},
					fixture.ReadFromPrimary(),
				)
				require.NoError(t, err)
				fallback, err := h.queries.GetUser(t.Context(), &fixture.GetUserParams{TenantID: 3, ID: 101})
				require.NoError(t, err)

				assert.Equal(t, "replica0", first.Name)
				assert.Equal(t, "replica1", second.Name)
				assert.Equal(t, "primary", strong.Name)
				assert.Equal(t, "shard1-primary", fallback.Name)
			},
		},
		{
			name: "writes route by virtual shard and mirror only shard zero",
			run: func(t *testing.T, h *postgresHarness) {
				_, err := h.queries.CreateUser(t.Context(), &fixture.CreateUserParams{ID: 200, TenantID: 2, Name: "even"})
				require.NoError(t, err)
				_, err = h.queries.CreateUser(t.Context(), &fixture.CreateUserParams{ID: 201, TenantID: 3, Name: "odd"})
				require.NoError(t, err)

				assert.Equal(t, "even", h.userName(t, "shard0-primary", 200, 2))
				assert.Equal(t, "even", h.userName(t, "shard0-mirror", 200, 2))
				h.assertUserAbsent(t, "shard0-replica0", 200, 2)
				h.assertUserAbsent(t, "shard0-replica1", 200, 2)
				assert.Equal(t, "odd", h.userName(t, "shard1-primary", 201, 3))
				h.assertUserAbsent(t, "shard0-primary", 201, 3)
				h.assertUserAbsent(t, "shard0-mirror", 201, 3)
			},
		},
		{
			name: "mirror error preserves committed primary result",
			run: func(t *testing.T, h *postgresHarness) {
				h.insert(t, "shard0-mirror", 300, 2, "existing")

				user, err := h.queries.CreateUser(
					t.Context(),
					&fixture.CreateUserParams{ID: 300, TenantID: 2, Name: "primary-result"},
				)
				require.Error(t, err)
				var pgErr *pgconn.PgError
				require.ErrorAs(t, err, &pgErr)
				assert.Equal(t, "23505", pgErr.Code)
				require.NotNil(t, user)
				assert.Equal(t, "primary-result", user.Name)
				assert.Equal(t, "primary-result", h.userName(t, "shard0-primary", 300, 2))
				assert.Equal(t, "existing", h.userName(t, "shard0-mirror", 300, 2))
			},
		},
		{
			name: "transaction pins primary and disables mirror",
			run: func(t *testing.T, h *postgresHarness) {
				tx, err := h.pools["shard0-primary"].Begin(t.Context())
				require.NoError(t, err)
				defer func() { _ = tx.Rollback(context.Background()) }()

				created, err := h.queries.CreateUser(
					t.Context(),
					&fixture.CreateUserParams{ID: 400, TenantID: 2, Name: "transactional"},
					fixture.WithTx(tx),
				)
				require.NoError(t, err)
				assert.Equal(t, "transactional", created.Name)
				inside, err := h.queries.GetUser(
					t.Context(),
					&fixture.GetUserParams{ID: 400, TenantID: 2},
					fixture.WithTx(tx),
				)
				require.NoError(t, err)
				assert.Equal(t, "transactional", inside.Name)
				require.NoError(t, tx.Commit(t.Context()))

				assert.Equal(t, "transactional", h.userName(t, "shard0-primary", 400, 2))
				h.assertUserAbsent(t, "shard0-mirror", 400, 2)
			},
		},
		{
			name: "manually partitioned copyfrom mirrors one shard",
			run: func(t *testing.T, h *postgresHarness) {
				shard, err := h.mesh.Shard(2)
				require.NoError(t, err)
				count, err := shard.Write().CopyUsers(t.Context(), []*fixture.CopyUsersParams{
					{ID: 500, TenantID: 2, Name: "copy-a"},
					{ID: 501, TenantID: 2, Name: "copy-b"},
				})
				require.NoError(t, err)
				assert.Equal(t, int64(2), count)
				assert.Equal(t, "copy-a", h.userName(t, "shard0-primary", 500, 2))
				assert.Equal(t, "copy-b", h.userName(t, "shard0-primary", 501, 2))
				assert.Equal(t, "copy-a", h.userName(t, "shard0-mirror", 500, 2))
				assert.Equal(t, "copy-b", h.userName(t, "shard0-mirror", 501, 2))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness.reset(t)
			test.run(t, harness)
		})
	}
}
