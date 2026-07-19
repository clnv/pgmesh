package pgmesh_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/clnv/pgmesh"
)

type fakeWriter struct {
	name    string
	mirrors []*fakeWriter
}

func (w *fakeWriter) WithMirrors(mirrors ...*fakeWriter) *fakeWriter {
	return &fakeWriter{name: w.name, mirrors: append(append([]*fakeWriter(nil), w.mirrors...), mirrors...)}
}

func node(name string) pgmesh.Node[string, *fakeWriter] {
	return pgmesh.NewNode(name+"-read", &fakeWriter{name: name + "-write"})
}

func TestShardHashers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hasher pgmesh.ShardHasher[uint64]
		key    uint64
		want   uint64
	}{
		{name: "constant ignores zero key", hasher: pgmesh.ConstantShardHashFor[uint64](7), key: 0, want: 7},
		{name: "constant ignores nonzero key", hasher: pgmesh.ConstantShardHashFor[uint64](7), key: 99, want: 7},
		{name: "modular zero", hasher: pgmesh.ModularShardHashFor[uint64](4), key: 0, want: 0},
		{name: "modular in range", hasher: pgmesh.ModularShardHashFor[uint64](4), key: 3, want: 3},
		{name: "modular wraps", hasher: pgmesh.ModularShardHashFor[uint64](4), key: 9, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, test.hasher.Hash(test.key))
		})
	}
}

func TestModularShardHashRejectsZeroVirtualShards(t *testing.T) {
	t.Parallel()
	require.PanicsWithValue(
		t,
		"pgmesh: numVShards must not be zero",
		func() { pgmesh.ModularShardHashFor[uint64](0) },
	)
}

func TestVShardRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from uint64
		to   uint64
		want []uint64
	}{
		{name: "empty equal bounds", from: 2, to: 2, want: []uint64{}},
		{name: "empty reversed bounds", from: 3, to: 2, want: []uint64{}},
		{name: "single", from: 2, to: 3, want: []uint64{2}},
		{name: "half open range", from: 2, to: 6, want: []uint64{2, 3, 4, 5}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, pgmesh.VShardRange(test.from, test.to))
		})
	}
}

func TestReplicaSetRoutesReadsAndWrites(t *testing.T) {
	t.Parallel()

	primary := node("primary")
	replica0 := node("replica0")
	replica1 := node("replica1")
	mirror0 := node("mirror0")
	mirror1 := node("mirror1")

	replicaSet := pgmesh.NewReplicaSet("main", primary, []pgmesh.Node[string, *fakeWriter]{replica0, replica1}).
		WithWriteMirrors(mirror0.Writer(), mirror1.Writer())

	assert.Equal(t, "replica0-read", replicaSet.Read())
	assert.Equal(t, "replica1-read", replicaSet.Read())
	assert.Equal(t, "replica0-read", replicaSet.Read())

	writer := replicaSet.Write()
	assert.Equal(t, "primary-write", writer.name)
	require.Len(t, writer.mirrors, 2)
	assert.Equal(t, "mirror0-write", writer.mirrors[0].name)
	assert.Equal(t, "mirror1-write", writer.mirrors[1].name)
	assert.Empty(t, primary.Writer().mirrors, "routing must not mutate the primary node")
	assert.Equal(t, 2, replicaSet.WriteMirrorCount())
}

func TestQueryTelemetryRecordsRoutingAndErrors(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, tracerProvider.Shutdown(context.Background())) })
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, meterProvider.Shutdown(context.Background())) })
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))

	replicaSet := pgmesh.NewReplicaSet("main", node("main"), nil).
		WithWriteMirrors(node("mirror").Writer())
	mesh, err := pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
		WithTracerProvider(tracerProvider).
		WithMeterProvider(meterProvider).
		WithLogger(logger).
		WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).
		Link(0, replicaSet).
		Build()
	require.NoError(t, err)

	ctx, queryTrace := mesh.StartQueryTrace(t.Context(), "CreateUser", pgmesh.QueryKindWrite)
	assert.True(t, trace.SpanFromContext(ctx).SpanContext().IsValid())
	queryTrace.SetRoute(0, "main", pgmesh.RouteModePrimary, replicaSet.WriteMirrorCount())
	queryErr := errors.New("write failed")
	queryTrace.End(queryErr)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, "pgmesh.query CreateUser", span.Name())
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, queryErr.Error(), span.Status().Description)

	attributes := make(map[attribute.Key]attribute.Value)
	for _, item := range span.Attributes() {
		attributes[item.Key] = item.Value
	}
	assert.Equal(t, "CreateUser", attributes[pgmesh.AttributeQueryName].AsString())
	assert.Equal(t, "write", attributes[pgmesh.AttributeQueryKind].AsString())
	assert.Equal(t, "0", attributes[pgmesh.AttributeVShard].AsString())
	assert.Equal(t, "main", attributes[pgmesh.AttributeReplicaSet].AsString())
	assert.Equal(t, "primary", attributes[pgmesh.AttributeRouteMode].AsString())
	assert.Equal(t, int64(1), attributes[pgmesh.AttributeWriteMirrorCount].AsInt64())
	require.Len(t, span.Events(), 1)
	assert.Equal(t, "exception", span.Events()[0].Name)

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &metrics))
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 2)
	for _, measurement := range metrics.ScopeMetrics[0].Metrics {
		switch measurement.Name {
		case pgmesh.MetricQueryCount:
			data, ok := measurement.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			require.Len(t, data.DataPoints, 1)
			assert.Equal(t, int64(1), data.DataPoints[0].Value)
			assertMetricAttributes(t, data.DataPoints[0].Attributes.ToSlice())
		case pgmesh.MetricQueryDuration:
			data, ok := measurement.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			require.Len(t, data.DataPoints, 1)
			assert.Equal(t, uint64(1), data.DataPoints[0].Count)
			assert.GreaterOrEqual(t, data.DataPoints[0].Sum, 0.0)
			assertMetricAttributes(t, data.DataPoints[0].Attributes.ToSlice())
		default:
			t.Fatalf("unexpected metric %q", measurement.Name)
		}
	}

	var logRecord map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(logOutput.Bytes()), &logRecord))
	assert.Equal(t, "DEBUG", logRecord["level"])
	assert.Equal(t, "pgmesh query completed", logRecord["msg"])
	assert.Equal(t, "CreateUser", logRecord["query_name"])
	assert.Equal(t, "write", logRecord["query_kind"])
	assert.Equal(t, true, logRecord["failed"])
	assert.Equal(t, "0", logRecord["vshard"])
	assert.Equal(t, "main", logRecord["replica_set"])
	assert.Equal(t, "primary", logRecord["route_mode"])
	assert.InDelta(t, 1, logRecord["write_mirror_count"], 0)
	assert.Equal(t, queryErr.Error(), logRecord["error"])
	assert.Contains(t, logRecord, "duration")
}

func assertMetricAttributes(t *testing.T, items []attribute.KeyValue) {
	t.Helper()

	attributes := make(map[attribute.Key]attribute.Value)
	for _, item := range items {
		attributes[item.Key] = item.Value
	}
	assert.Equal(t, "CreateUser", attributes[pgmesh.AttributeQueryName].AsString())
	assert.Equal(t, "write", attributes[pgmesh.AttributeQueryKind].AsString())
	assert.True(t, attributes[pgmesh.AttributeQueryError].AsBool())
	assert.Equal(t, "0", attributes[pgmesh.AttributeVShard].AsString())
	assert.Equal(t, "main", attributes[pgmesh.AttributeReplicaSet].AsString())
	assert.Equal(t, "primary", attributes[pgmesh.AttributeRouteMode].AsString())
	assert.Equal(t, int64(1), attributes[pgmesh.AttributeWriteMirrorCount].AsInt64())
}

func TestReplicaSetFallsBackToPrimaryReader(t *testing.T) {
	t.Parallel()

	replicaSet := pgmesh.NewReplicaSet("main", node("primary"), nil)
	assert.Equal(t, "primary-read", replicaSet.Read())
}

func TestReplicaSetRoundRobinIsConcurrent(t *testing.T) {
	t.Parallel()

	replicaSet := pgmesh.NewReplicaSet(
		"main",
		node("primary"),
		[]pgmesh.Node[string, *fakeWriter]{node("replica0"), node("replica1")},
	)

	const calls = 1000
	results := make(chan string, calls)
	var group sync.WaitGroup
	for range calls {
		group.Go(func() {
			results <- replicaSet.Read()
		})
	}
	group.Wait()
	close(results)

	counts := map[string]int{}
	for result := range results {
		counts[result]++
	}
	assert.Equal(t, calls/2, counts["replica0-read"])
	assert.Equal(t, calls/2, counts["replica1-read"])
}

func TestBuilderRoutesAndListsPhysicalShardsDeterministically(t *testing.T) {
	t.Parallel()

	shardA := pgmesh.NewReplicaSet("a", node("a"), nil)
	shardB := pgmesh.NewReplicaSet("b", node("b"), nil)
	mesh, err := pgmesh.NewBuilder[string, *fakeWriter, uint64](4).
		WithHasher(pgmesh.ModularShardHashFor[uint64](4)).
		Link(0, shardB).
		Link(1, shardA).
		Link(2, shardB).
		Link(3, shardA).
		Build()
	require.NoError(t, err)

	routed, err := mesh.Shard(5)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), routed.VShardIndex())
	assert.Equal(t, "a", routed.Name())

	all := mesh.AllShards()
	require.Len(t, all, 2)
	assert.Equal(t, "b", all[0].Name())
	assert.Equal(t, "a", all[1].Name())
	all[0] = nil
	assert.NotNil(t, mesh.AllShards()[0], "AllShards must return a defensive slice")
}

func TestMeshRejectsOutOfRangeHasherResult(t *testing.T) {
	t.Parallel()

	mesh, err := pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
		WithHasher(pgmesh.ConstantShardHashFor[uint64](2)).
		Link(0, pgmesh.NewReplicaSet("main", node("main"), nil)).
		Build()
	require.NoError(t, err)

	_, err = mesh.Shard(1)
	assert.ErrorIs(t, err, pgmesh.ErrVShardOutOfRange)
}

func TestBuilderValidation(t *testing.T) {
	t.Parallel()

	replicaSet := pgmesh.NewReplicaSet("main", node("main"), nil)
	tests := []struct {
		name string
		make func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error)
		want error
	}{
		{
			name: "no virtual shards",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](0).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).Build()
			},
			want: pgmesh.ErrNoVShards,
		},
		{
			name: "no hasher",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).Link(0, replicaSet).Build()
			},
			want: pgmesh.ErrNoShardHasher,
		},
		{
			name: "missing virtual shard",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).Build()
			},
			want: pgmesh.ErrMissingVShard,
		},
		{
			name: "duplicate virtual shard",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).
					Link(0, replicaSet).Link(0, replicaSet).Build()
			},
			want: pgmesh.ErrDuplicateVShard,
		},
		{
			name: "link out of range",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).Link(1, replicaSet).Build()
			},
			want: pgmesh.ErrVShardOutOfRange,
		},
		{
			name: "empty replica set name",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).
					Link(0, pgmesh.NewReplicaSet("", node("main"), nil)).Build()
			},
			want: pgmesh.ErrEmptyReplicaSetName,
		},
		{
			name: "nil replica set",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).Link(0, nil).Build()
			},
			want: pgmesh.ErrNilReplicaSet,
		},
		{
			name: "duplicate physical name",
			make: func() (*pgmesh.Mesh[string, *fakeWriter, uint64], error) {
				return pgmesh.NewBuilder[string, *fakeWriter, uint64](2).
					WithHasher(pgmesh.ConstantShardHashFor[uint64](0)).
					Link(0, pgmesh.NewReplicaSet("same", node("a"), nil)).
					Link(1, pgmesh.NewReplicaSet("same", node("b"), nil)).Build()
			},
			want: pgmesh.ErrDuplicateReplicaSet,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.make()
			assert.ErrorIs(t, err, test.want)
		})
	}
}

func TestCreateMeshBuildsTopologyAndMirrors(t *testing.T) {
	t.Parallel()

	created := make([]string, 0)
	mesh, err := pgmesh.CreateMesh(t.Context(), &pgmesh.Options[string, *fakeWriter, uint64]{
		ReplicaSets: []pgmesh.ReplicaSetSpec{
			{Name: "a", Primary: pgmesh.Connection{DSN: "a-primary"}, Replicas: []pgmesh.Connection{{DSN: "a-replica"}}},
			{Name: "b", Primary: pgmesh.Connection{DSN: "b-primary"}},
		},
		Shards: pgmesh.Shards{
			NumVShards: 2,
			Mappings: []pgmesh.VShardMapping{
				{VShards: []uint64{0}, MainReplicaSet: "a", MirrorReplicaSets: []string{"b"}},
				{VShards: []uint64{1}, MainReplicaSet: "b"},
			},
		},
		CreateNode: func(_ context.Context, dsn string) (pgmesh.Node[string, *fakeWriter], error) {
			created = append(created, dsn)
			return node(dsn), nil
		},
		ShardHasher: pgmesh.ModularShardHashFor[uint64](2),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a-primary", "a-replica", "b-primary"}, created)

	shard, err := mesh.Shard(0)
	require.NoError(t, err)
	assert.Equal(t, "a-replica-read", shard.Read())
	writer := shard.Write()
	require.Len(t, writer.mirrors, 1)
	assert.Equal(t, "b-primary-write", writer.mirrors[0].name)
}

func TestCreateMeshValidation(t *testing.T) {
	t.Parallel()
	_, err := pgmesh.CreateMesh[string, *fakeWriter, uint64](t.Context(), nil)
	require.ErrorIs(t, err, pgmesh.ErrNoReplicaSets)

	valid := func() *pgmesh.Options[string, *fakeWriter, uint64] {
		return &pgmesh.Options[string, *fakeWriter, uint64]{
			ReplicaSets: []pgmesh.ReplicaSetSpec{{
				Name:    "main",
				Primary: pgmesh.Connection{DSN: "primary"},
			}},
			Shards: pgmesh.Shards{
				NumVShards: 1,
				Mappings:   []pgmesh.VShardMapping{{VShards: []uint64{0}, MainReplicaSet: "main"}},
			},
			CreateNode: func(context.Context, string) (pgmesh.Node[string, *fakeWriter], error) {
				return node("main"), nil
			},
			ShardHasher: pgmesh.ConstantShardHashFor[uint64](0),
		}
	}

	tests := []struct {
		name string
		edit func(*pgmesh.Options[string, *fakeWriter, uint64])
		want error
	}{
		{
			name: "no replica sets",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ReplicaSets = nil },
			want: pgmesh.ErrNoReplicaSets,
		},
		{
			name: "empty name",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Name = "" },
			want: pgmesh.ErrEmptyReplicaSetName,
		},
		{
			name: "whitespace name",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Name = " \t" },
			want: pgmesh.ErrEmptyReplicaSetName,
		},
		{name: "duplicate name", edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
			o.ReplicaSets = append(o.ReplicaSets, o.ReplicaSets[0])
		}, want: pgmesh.ErrDuplicateReplicaSet},
		{
			name: "empty DSN",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Primary.DSN = "" },
			want: pgmesh.ErrEmptyDSN,
		},
		{
			name: "whitespace primary DSN",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Primary.DSN = " \t" },
			want: pgmesh.ErrEmptyDSN,
		},
		{
			name: "empty replica DSN",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
				o.ReplicaSets[0].Replicas = []pgmesh.Connection{{DSN: ""}}
			},
			want: pgmesh.ErrEmptyDSN,
		},
		{
			name: "no factory",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.CreateNode = nil },
			want: pgmesh.ErrNoNodeFactory,
		},
		{
			name: "no hasher",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.ShardHasher = nil },
			want: pgmesh.ErrNoShardHasher,
		},
		{
			name: "no virtual shards",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.Shards.NumVShards = 0 },
			want: pgmesh.ErrNoVShards,
		},
		{name: "unknown main", edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings[0].MainReplicaSet = "missing"
		}, want: pgmesh.ErrUnknownReplicaSet},
		{name: "unknown mirror", edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings[0].MirrorReplicaSets = []string{"missing"}
		}, want: pgmesh.ErrUnknownReplicaSet},
		{
			name: "self mirror",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
				o.Shards.Mappings[0].MirrorReplicaSets = []string{"main"}
			},
			want: pgmesh.ErrMirrorConfiguration,
		},
		{
			name: "duplicate mirror",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
				o.ReplicaSets = append(
					o.ReplicaSets,
					pgmesh.ReplicaSetSpec{Name: "mirror", Primary: pgmesh.Connection{DSN: "mirror"}},
				)
				o.Shards.Mappings[0].MirrorReplicaSets = []string{"mirror", "mirror"}
			},
			want: pgmesh.ErrMirrorConfiguration,
		},
		{
			name: "missing vshard",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.Shards.Mappings = nil },
			want: pgmesh.ErrMissingVShard,
		},
		{name: "duplicate vshard", edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings = append(o.Shards.Mappings, o.Shards.Mappings[0])
		}, want: pgmesh.ErrDuplicateVShard},
		{
			name: "out of range",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) { o.Shards.Mappings[0].VShards = []uint64{1} },
			want: pgmesh.ErrVShardOutOfRange,
		},
		{
			name: "inconsistent mirrors",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
				o.Shards.NumVShards = 2
				o.ReplicaSets = append(
					o.ReplicaSets,
					pgmesh.ReplicaSetSpec{Name: "mirror", Primary: pgmesh.Connection{DSN: "mirror"}},
				)
				o.Shards.Mappings = []pgmesh.VShardMapping{
					{VShards: []uint64{0}, MainReplicaSet: "main"},
					{VShards: []uint64{1}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror"}},
				}
			},
			want: pgmesh.ErrMirrorConfiguration,
		},
		{
			name: "inconsistent mirror order",
			edit: func(o *pgmesh.Options[string, *fakeWriter, uint64]) {
				o.Shards.NumVShards = 2
				o.ReplicaSets = append(
					o.ReplicaSets,
					pgmesh.ReplicaSetSpec{Name: "mirror-a", Primary: pgmesh.Connection{DSN: "mirror-a"}},
					pgmesh.ReplicaSetSpec{Name: "mirror-b", Primary: pgmesh.Connection{DSN: "mirror-b"}},
				)
				o.Shards.Mappings = []pgmesh.VShardMapping{
					{VShards: []uint64{0}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror-a", "mirror-b"}},
					{VShards: []uint64{1}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror-b", "mirror-a"}},
				}
			},
			want: pgmesh.ErrMirrorConfiguration,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts := valid()
			test.edit(opts)
			_, err := pgmesh.CreateMesh(t.Context(), opts)
			assert.ErrorIs(t, err, test.want)
		})
	}
}

func TestCreateMeshWrapsFactoryError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("connect failed")
	_, err := pgmesh.CreateMesh(t.Context(), &pgmesh.Options[string, *fakeWriter, uint64]{
		ReplicaSets: []pgmesh.ReplicaSetSpec{{Name: "main", Primary: pgmesh.Connection{DSN: "primary"}}},
		Shards: pgmesh.Shards{NumVShards: 1, Mappings: []pgmesh.VShardMapping{{
			VShards: []uint64{0}, MainReplicaSet: "main",
		}}},
		CreateNode: func(context.Context, string) (pgmesh.Node[string, *fakeWriter], error) {
			return pgmesh.Node[string, *fakeWriter]{}, sentinel
		},
		ShardHasher: pgmesh.ConstantShardHashFor[uint64](0),
	})
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), fmt.Sprintf("primary node for replica set %q", "main"))
}
