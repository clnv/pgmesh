package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/clnv/pgmesh"
)

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, name)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

type fakeDB struct {
	name   string
	log    *callLog
	rowErr error
}

func (db *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	db.log.add(db.name)
	return pgconn.CommandTag{}, nil
}

func (db *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	db.log.add(db.name)
	return nil, errors.New("fake rows are not configured")
}

func (db *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	db.log.add(db.name)
	return fakeRow{err: db.rowErr}
}

func (db *fakeDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	db.log.add(db.name)
	return 1, nil
}

type fakeRow struct {
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 3 {
		id, ok := dest[0].(*int64)
		if !ok {
			return fmt.Errorf("destination 0 has type %T, want *int64", dest[0])
		}
		tenantID, ok := dest[1].(*int64)
		if !ok {
			return fmt.Errorf("destination 1 has type %T, want *int64", dest[1])
		}
		name, ok := dest[2].(*string)
		if !ok {
			return fmt.Errorf("destination 2 has type %T, want *string", dest[2])
		}
		*id = 10
		*tenantID = 20
		*name = "user"
	}
	return nil
}

type fakeTx struct {
	*fakeDB
}

func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return tx, nil
}

func (tx *fakeTx) Commit(context.Context) error {
	return nil
}

func (tx *fakeTx) Rollback(context.Context) error {
	return nil
}

func (tx *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *fakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (tx *fakeTx) Conn() *pgx.Conn {
	return nil
}

type tenantResolver struct{}

func (tenantResolver) Tenant(int64) uint64 {
	return 0
}

func (tenantResolver) Messagekey(int64, int64, bool) uint64 {
	return 0
}

type recordingTenantResolver struct {
	tenantID *int64
}

func (r recordingTenantResolver) Tenant(tenantID int64) uint64 {
	*r.tenantID = tenantID
	return 0
}

func (recordingTenantResolver) Messagekey(int64, int64, bool) uint64 {
	return 0
}

type recordingMessageKeyResolver struct {
	userID          int64
	toUserOrGroupID int64
	inGroup         bool
}

func (recordingMessageKeyResolver) Tenant(int64) uint64 {
	return 0
}

func (r *recordingMessageKeyResolver) Messagekey(
	userID int64,
	toUserOrGroupID int64,
	inGroup bool,
) uint64 {
	r.userID = userID
	r.toUserOrGroupID = toUserOrGroupID
	r.inGroup = inGroup
	return 0
}

func buildTestStore(t *testing.T, primary, replica *fakeDB, mirrors ...*fakeDB) Store {
	t.Helper()

	options := []ShardedOption{WithReplicaSet("main", primary, replica)}
	mirrorNames := make([]string, 0, len(mirrors))
	for index, mirror := range mirrors {
		if mirror != nil {
			name := fmt.Sprintf("mirror-%d", index)
			options = append(options, WithReplicaSet(name, mirror))
			mirrorNames = append(mirrorNames, name)
		}
	}
	options = append(options, WithVShardMapping("main", []uint64{0}, mirrorNames...))
	store, err := NewStore(
		t.Context(),
		Sharded(
			1,
			pgmesh.ConstantShardHashFor[uint64](0),
			tenantResolver{},
			options...,
		),
	)
	require.NoError(t, err)
	return store
}

func TestGeneratedRoutingOnlyShardArgument(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	var resolvedTenantID int64
	store, err := NewStore(
		t.Context(),
		Sharded(
			1,
			pgmesh.ConstantShardHashFor[uint64](0),
			recordingTenantResolver{tenantID: &resolvedTenantID},
			WithReplicaSet(
				"main",
				&fakeDB{name: "primary", log: log},
				&fakeDB{name: "replica", log: log},
			),
			WithVShardMapping("main", []uint64{0}),
		),
	)
	require.NoError(t, err)

	row, err := store.Analyses().GetTenantUserAnalysis(
		t.Context(),
		&GetTenantUserAnalysisShardParams{
			Arg: &GetTenantUserAnalysisParams{
				UserID:     10,
				AnalysisID: 20,
			},
			TenantID: 42,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int64(42), resolvedTenantID)
	assert.Equal(t, []string{"replica"}, log.snapshot())
}

func TestGeneratedP2PShardArgumentUsesMessageModel(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	resolver := &recordingMessageKeyResolver{}
	store, err := NewStore(
		t.Context(),
		Sharded(
			1,
			pgmesh.ConstantShardHashFor[uint64](0),
			resolver,
			WithReplicaSet(
				"main",
				&fakeDB{name: "primary", log: log},
				&fakeDB{name: "replica", log: log},
			),
			WithVShardMapping("main", []uint64{0}),
		),
	)
	require.NoError(t, err)

	_, err = store.QueryMessage().ListP2PMessageIDsByChat(
		t.Context(),
		&ListP2PMessageIDsByChatShardParams{
			Arg:             &ListP2PMessageIDsByChatParams{UserID: 11},
			ToUserOrGroupID: 22,
			InGroup:         false,
		},
	)
	require.ErrorContains(t, err, "fake rows are not configured")
	assert.Equal(t, int64(11), resolver.userID)
	assert.Equal(t, int64(22), resolver.toUserOrGroupID)
	assert.False(t, resolver.inGroup)
	assert.Equal(t, []string{"replica"}, log.snapshot())
}

func TestGeneratedTopologyOptionsCloneInputs(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	primary := &fakeDB{name: "primary", log: log}
	replica := &fakeDB{name: "replica", log: log}
	mirror := &fakeDB{name: "mirror", log: log}
	replicas := []DBTX{replica}
	vshards := []uint64{0}
	mirrorNames := []string{"mirror"}
	replicaSetOption := WithReplicaSet("main", primary, replicas...)
	mappingOption := WithVShardMapping("main", vshards, mirrorNames...)

	replicas[0] = nil
	vshards[0] = 1
	mirrorNames[0] = "missing"

	store, err := NewStore(
		t.Context(),
		Sharded(
			1,
			pgmesh.ConstantShardHashFor[uint64](0),
			tenantResolver{},
			replicaSetOption,
			WithReplicaSet("mirror", mirror),
			mappingOption,
		),
	)
	require.NoError(t, err)

	_, err = store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2})
	require.NoError(t, err)
	_, err = store.Users().CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
	require.NoError(t, err)
	assert.Equal(t, []string{"replica", "primary", "mirror"}, log.snapshot())
}

func TestGeneratedStoreTelemetryWiring(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, tracerProvider.Shutdown(context.Background())) })
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, meterProvider.Shutdown(context.Background())) })
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))

	callLog := &callLog{}
	mirrorErr := errors.New("mirror unavailable")
	store, err := NewStore(
		t.Context(),
		Sharded(
			1,
			pgmesh.ConstantShardHashFor[uint64](0),
			tenantResolver{},
			WithReplicaSet(
				"main",
				&fakeDB{name: "primary", log: callLog},
				&fakeDB{name: "replica", log: callLog},
			),
			WithReplicaSet("mirror", &fakeDB{name: "mirror", log: callLog, rowErr: mirrorErr}),
			WithVShardMapping("main", []uint64{0}, "mirror"),
		),
		WithTracerProvider(tracerProvider),
		WithMeterProvider(meterProvider),
		WithLogger(logger),
	)
	require.NoError(t, err)

	_, err = store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2})
	require.NoError(t, err)
	_, err = store.Users().GetUser(
		t.Context(),
		&GetUserParams{TenantID: 1, ID: 2},
		ReadFromPrimary(),
	)
	require.NoError(t, err)
	tx := &fakeTx{fakeDB: &fakeDB{name: "tx", log: callLog}}
	_, err = store.Users().GetUser(
		t.Context(),
		&GetUserParams{TenantID: 1, ID: 2},
		WithTx(tx),
	)
	require.NoError(t, err)
	user, err := store.Users().CreateUser(
		t.Context(),
		&CreateUserParams{ID: 1, TenantID: 2, Name: "user"},
	)
	require.ErrorIs(t, err, mirrorErr)
	require.NotNil(t, user)

	type spanExpectation struct {
		query       string
		kind        string
		mode        string
		mirrorCount int64
		status      codes.Code
	}
	expectedSpans := []spanExpectation{
		{query: "GetUser", kind: "read", mode: "read"},
		{query: "GetUser", kind: "read", mode: "primary"},
		{query: "GetUser", kind: "read", mode: "transaction"},
		{query: "CreateUser", kind: "write", mode: "primary", mirrorCount: 1, status: codes.Error},
	}
	spans := recorder.Ended()
	require.Len(t, spans, len(expectedSpans))
	for index, expected := range expectedSpans {
		attributes := telemetryAttributeMap(spans[index].Attributes())
		assert.Equal(t, expected.query, attributes[pgmesh.AttributeQueryName].AsString())
		assert.Equal(t, expected.kind, attributes[pgmesh.AttributeQueryKind].AsString())
		assert.Equal(t, "main", attributes[pgmesh.AttributeReplicaSet].AsString())
		assert.Equal(t, expected.mode, attributes[pgmesh.AttributeRouteMode].AsString())
		assert.Equal(t, expected.mirrorCount, attributes[pgmesh.AttributeWriteMirrorCount].AsInt64())
		assert.Equal(t, expected.status, spans[index].Status().Code)
	}

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &metrics))
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 1)
	histogram, ok := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, histogram.DataPoints, len(expectedSpans))
	var measurementCount uint64
	for _, point := range histogram.DataPoints {
		measurementCount += point.Count
	}
	assert.Equal(t, uint64(len(expectedSpans)), measurementCount)

	logLines := strings.Split(strings.TrimSpace(logOutput.String()), "\n")
	require.Len(t, logLines, len(expectedSpans))
	for index, line := range logLines {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		assert.Equal(t, expectedSpans[index].query, record["query_name"])
		assert.Equal(t, expectedSpans[index].mode, record["route_mode"])
		mirrorCount, ok := record["write_mirror_count"].(float64)
		require.True(t, ok)
		assert.InDelta(t, expectedSpans[index].mirrorCount, mirrorCount, 0)
		assert.Equal(t, expectedSpans[index].status == codes.Error, record["failed"])
	}
}

func telemetryAttributeMap(items []attribute.KeyValue) map[attribute.Key]attribute.Value {
	attributes := make(map[attribute.Key]attribute.Value, len(items))
	for _, item := range items {
		attributes[item.Key] = item.Value
	}
	return attributes
}

func TestGeneratedStoreBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "primary and replica selection",
			run: func(t *testing.T) {
				log := &callLog{}
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
				)

				_, err := store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2})
				require.NoError(t, err)
				_, err = store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2}, ReadFromPrimary())
				require.NoError(t, err)
				assert.Equal(t, []string{"replica", "primary"}, log.snapshot())
			},
		},
		{
			name: "nil query option",
			run: func(t *testing.T) {
				log := &callLog{}
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
				)

				_, err := store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2}, nil)
				require.NoError(t, err)
				assert.Equal(t, []string{"replica"}, log.snapshot())
			},
		},
		{
			name: "missing mirror row is ignored",
			run: func(t *testing.T) {
				log := &callLog{}
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
					&fakeDB{name: "mirror", log: log, rowErr: pgx.ErrNoRows},
					&fakeDB{name: "mirror-after-missing", log: log},
				)

				user, err := store.Users().CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
				require.NoError(t, err)
				require.NotNil(t, user)
				assert.Equal(t, []string{"primary", "mirror", "mirror-after-missing"}, log.snapshot())
			},
		},
		{
			name: "mirrors succeed in order",
			run: func(t *testing.T) {
				log := &callLog{}
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
					&fakeDB{name: "mirror0", log: log},
					&fakeDB{name: "mirror1", log: log},
				)

				_, err := store.Users().CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
				require.NoError(t, err)
				assert.Equal(t, []string{"primary", "mirror0", "mirror1"}, log.snapshot())
			},
		},
		{
			name: "mirror failure preserves primary result",
			run: func(t *testing.T) {
				log := &callLog{}
				mirrorErr := errors.New("mirror unavailable")
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
					&fakeDB{name: "mirror", log: log, rowErr: mirrorErr},
					&fakeDB{name: "mirror-not-called", log: log},
				)

				user, err := store.Users().CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
				require.ErrorIs(t, err, mirrorErr)
				require.NotNil(t, user, "primary result must be retained when a mirror fails")
				assert.Equal(t, []string{"primary", "mirror"}, log.snapshot())
			},
		},
		{
			name: "primary failure skips mirrors",
			run: func(t *testing.T) {
				log := &callLog{}
				primaryErr := errors.New("primary unavailable")
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log, rowErr: primaryErr},
					&fakeDB{name: "replica", log: log},
					&fakeDB{name: "mirror", log: log},
				)

				user, err := store.Users().CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
				require.ErrorIs(t, err, primaryErr)
				assert.Nil(t, user)
				assert.Equal(t, []string{"primary"}, log.snapshot())
			},
		},
		{
			name: "transaction pins primary and drops mirrors",
			run: func(t *testing.T) {
				log := &callLog{}
				store := buildTestStore(
					t,
					&fakeDB{name: "primary", log: log},
					&fakeDB{name: "replica", log: log},
					&fakeDB{name: "mirror", log: log},
				)
				tx := &fakeTx{fakeDB: &fakeDB{name: "tx", log: log}}

				_, err := store.Users().GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2}, WithTx(tx))
				require.NoError(t, err)
				_, err = store.Users().CreateUser(
					t.Context(),
					&CreateUserParams{ID: 1, TenantID: 2, Name: "user"},
					WithTx(tx),
				)
				require.NoError(t, err)
				assert.Equal(t, []string{"tx", "tx"}, log.snapshot())
			},
		},
		{
			name: "invalid database configurations",
			run: func(t *testing.T) {
				log := &callLog{}
				primary := &fakeDB{name: "primary", log: log}
				tests := []struct {
					name    string
					config  Topology
					options []StoreOption
					want    string
				}{
					{name: "nil topology", want: "topology is nil"},
					{name: "nil primary", config: Singleton(nil), want: "database primary is nil"},
					{
						name:   "nil singleton option",
						config: Singleton(primary, nil),
						want:   "singleton option 0 is nil",
					},
					{
						name:    "nil store option",
						config:  Singleton(primary),
						options: []StoreOption{nil},
						want:    "store option 0 is nil",
					},
					{
						name:   "nil replica",
						config: Singleton(primary, WithReadReplicas(nil)),
						want:   "database replica 0 is nil",
					},
					{
						name:   "nil mirror",
						config: Singleton(primary, WithWriteMirrors(nil)),
						want:   "database mirror 0 is nil",
					},
				}
				for _, test := range tests {
					t.Run(test.name, func(t *testing.T) {
						_, err := NewStore(t.Context(), test.config, test.options...)
						require.ErrorContains(t, err, test.want)
					})
				}
			},
		},
		{
			name: "invalid sharded configurations",
			run: func(t *testing.T) {
				_, err := NewStore(t.Context(), Sharded[uint64](0, nil, nil))
				require.ErrorContains(t, err, "shard resolver is nil")

				_, err = NewStore(
					t.Context(),
					Sharded(
						1,
						pgmesh.ConstantShardHashFor[uint64](0),
						tenantResolver{},
						nil,
					),
				)
				require.ErrorContains(t, err, "sharded option 0 is nil")

				_, err = NewStore(
					t.Context(),
					Sharded(
						1,
						pgmesh.ConstantShardHashFor[uint64](0),
						tenantResolver{},
						WithReplicaSet("main", nil),
						WithVShardMapping("main", []uint64{0}),
					),
				)
				require.ErrorContains(t, err, "database node")
				require.ErrorContains(t, err, "is nil")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.run(t)
		})
	}
}
