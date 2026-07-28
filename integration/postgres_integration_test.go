//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/netip"
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
	queries fixture.Store
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
	for name, dsn := range dsns {
		pool, err := pgxpool.New(t.Context(), dsn)
		require.NoError(t, err, "create pool for %s", name)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		err = pool.Ping(ctx)
		cancel()
		require.NoError(t, err, "ping %s", name)
		pools[name] = pool
	}
	queries, err := fixture.NewStore(
		t.Context(),
		fixture.Sharded(
			2,
			pgmesh.ModularShardHashFor[uint64](2),
			tenantResolver{},
			fixture.WithReplicaSet(
				"shard0",
				pools["shard0-primary"],
				pools["shard0-replica0"],
				pools["shard0-replica1"],
			),
			fixture.WithReplicaSet("shard1", pools["shard1-primary"]),
			fixture.WithReplicaSet("shard0-mirror", pools["shard0-mirror"]),
			fixture.WithVShardMapping("shard0", []uint64{0}, "shard0-mirror"),
			fixture.WithVShardMapping("shard1", []uint64{1}),
		),
	)
	require.NoError(t, err)

	return &postgresHarness{
		queries: queries,
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
		_, err := pool.Exec(t.Context(), "TRUNCATE TABLE analyses, users")
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

				first, err := h.queries.Users().GetUser(t.Context(), &fixture.GetUserParams{TenantID: 2, ID: 100})
				require.NoError(t, err)
				second, err := h.queries.Users().GetUser(t.Context(), &fixture.GetUserParams{TenantID: 2, ID: 100})
				require.NoError(t, err)
				strong, err := h.queries.Users().GetUser(
					t.Context(),
					&fixture.GetUserParams{TenantID: 2, ID: 100},
					fixture.ReadFromPrimary(),
				)
				require.NoError(t, err)
				fallback, err := h.queries.Users().GetUser(t.Context(), &fixture.GetUserParams{TenantID: 3, ID: 101})
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
				_, err := h.queries.Users().CreateUser(t.Context(), &fixture.CreateUserParams{ID: 200, TenantID: 2, Name: "even"})
				require.NoError(t, err)
				_, err = h.queries.Users().CreateUser(t.Context(), &fixture.CreateUserParams{ID: 201, TenantID: 3, Name: "odd"})
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

				user, err := h.queries.Users().CreateUser(
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
			name: "missing mirror update row is ignored",
			run: func(t *testing.T, h *postgresHarness) {
				h.insert(t, "shard0-primary", 350, 2, "before")

				user, err := h.queries.Users().UpdateUserName(
					t.Context(),
					&fixture.UpdateUserNameParams{TenantID: 2, ID: 350, Name: "after"},
				)

				require.NoError(t, err)
				require.NotNil(t, user)
				assert.Equal(t, "after", user.Name)
				assert.Equal(t, "after", h.userName(t, "shard0-primary", 350, 2))
				h.assertUserAbsent(t, "shard0-mirror", 350, 2)
			},
		},
		{
			name: "analysis scans nullable network and range types",
			run: func(t *testing.T, h *postgresHarness) {
				_, err := h.pools["shard0-primary"].Exec(
					t.Context(),
					`INSERT INTO analyses (id, tenant_id, summary, state, source, active_window)
					 VALUES
					 (360, 2, 'ready', 'complete', '192.0.2.10', '[2026-01-02 03:04:05+00,2026-01-03 03:04:05+00)'),
					 (361, 2, NULL, NULL, '2001:db8::10', '[2026-02-02 03:04:05+00,2026-02-03 03:04:05+00)')`,
				)
				require.NoError(t, err)

				populated, err := h.queries.Analyses().GetAnalysis(
					t.Context(),
					&fixture.GetAnalysisParams{TenantID: 2, ID: 360},
					fixture.ReadFromPrimary(),
				)
				require.NoError(t, err)
				require.NotNil(t, populated.Description)
				assert.Equal(t, "ready", *populated.Description)
				assert.True(t, populated.State.Valid)
				assert.Equal(t, fixture.AnalysisStateComplete, populated.State.AnalysisState)
				assert.Equal(t, netip.MustParseAddr("192.0.2.10"), populated.Source)
				assert.True(t, populated.ActiveWindow.Valid)
				assert.Equal(t, "2026-01-02T03:04:05Z", populated.ActiveWindow.Lower.Time.UTC().Format(time.RFC3339))
				assert.Equal(t, "2026-01-03T03:04:05Z", populated.ActiveWindow.Upper.Time.UTC().Format(time.RFC3339))

				nullable, err := h.queries.Analyses().GetAnalysis(
					t.Context(),
					&fixture.GetAnalysisParams{TenantID: 2, ID: 361},
					fixture.ReadFromPrimary(),
				)
				require.NoError(t, err)
				assert.Nil(t, nullable.Description)
				assert.False(t, nullable.State.Valid)
				assert.Equal(t, netip.MustParseAddr("2001:db8::10"), nullable.Source)
				assert.True(t, nullable.ActiveWindow.Valid)
			},
		},
		{
			name: "transaction pins primary and disables mirror",
			run: func(t *testing.T, h *postgresHarness) {
				tx, err := h.pools["shard0-primary"].Begin(t.Context())
				require.NoError(t, err)
				defer func() { _ = tx.Rollback(context.Background()) }()

				created, err := h.queries.Users().CreateUser(
					t.Context(),
					&fixture.CreateUserParams{ID: 400, TenantID: 2, Name: "transactional"},
					fixture.WithTx(tx),
				)
				require.NoError(t, err)
				assert.Equal(t, "transactional", created.Name)
				inside, err := h.queries.Users().GetUser(
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness.reset(t)
			test.run(t, harness)
		})
	}
}
