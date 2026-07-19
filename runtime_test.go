package sqlcstore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/clnv/pgmesh"
)

type fakeWriter struct {
	name    string
	mirrors []*fakeWriter
}

func (w *fakeWriter) WithMirrors(mirrors ...*fakeWriter) *fakeWriter {
	return &fakeWriter{name: w.name, mirrors: append(append([]*fakeWriter(nil), w.mirrors...), mirrors...)}
}

func node(name string) sqlcstore.Node[string, *fakeWriter] {
	return sqlcstore.NewNode(name+"-read", &fakeWriter{name: name + "-write"})
}

func TestShardHashers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hasher sqlcstore.ShardHasher[uint64]
		key    uint64
		want   uint64
	}{
		{name: "constant ignores zero key", hasher: sqlcstore.ConstantShardHashFor[uint64](7), key: 0, want: 7},
		{name: "constant ignores nonzero key", hasher: sqlcstore.ConstantShardHashFor[uint64](7), key: 99, want: 7},
		{name: "modular zero", hasher: sqlcstore.ModularShardHashFor[uint64](4), key: 0, want: 0},
		{name: "modular in range", hasher: sqlcstore.ModularShardHashFor[uint64](4), key: 3, want: 3},
		{name: "modular wraps", hasher: sqlcstore.ModularShardHashFor[uint64](4), key: 9, want: 1},
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
		"sqlcstore: numVShards must not be zero",
		func() { sqlcstore.ModularShardHashFor[uint64](0) },
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
			assert.Equal(t, test.want, sqlcstore.VShardRange(test.from, test.to))
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

	replicaSet := sqlcstore.NewReplicaSet("main", primary, []sqlcstore.Node[string, *fakeWriter]{replica0, replica1}).
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
}

func TestReplicaSetFallsBackToPrimaryReader(t *testing.T) {
	t.Parallel()

	replicaSet := sqlcstore.NewReplicaSet("main", node("primary"), nil)
	assert.Equal(t, "primary-read", replicaSet.Read())
}

func TestReplicaSetRoundRobinIsConcurrent(t *testing.T) {
	t.Parallel()

	replicaSet := sqlcstore.NewReplicaSet(
		"main",
		node("primary"),
		[]sqlcstore.Node[string, *fakeWriter]{node("replica0"), node("replica1")},
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

	shardA := sqlcstore.NewReplicaSet("a", node("a"), nil)
	shardB := sqlcstore.NewReplicaSet("b", node("b"), nil)
	mesh, err := sqlcstore.NewBuilder[string, *fakeWriter, uint64](4).
		WithHasher(sqlcstore.ModularShardHashFor[uint64](4)).
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

	mesh, err := sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
		WithHasher(sqlcstore.ConstantShardHashFor[uint64](2)).
		Link(0, sqlcstore.NewReplicaSet("main", node("main"), nil)).
		Build()
	require.NoError(t, err)

	_, err = mesh.Shard(1)
	assert.ErrorIs(t, err, sqlcstore.ErrVShardOutOfRange)
}

func TestBuilderValidation(t *testing.T) {
	t.Parallel()

	replicaSet := sqlcstore.NewReplicaSet("main", node("main"), nil)
	tests := []struct {
		name string
		make func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error)
		want error
	}{
		{
			name: "no virtual shards",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](0).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).Build()
			},
			want: sqlcstore.ErrNoVShards,
		},
		{
			name: "no hasher",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).Link(0, replicaSet).Build()
			},
			want: sqlcstore.ErrNoShardHasher,
		},
		{
			name: "missing virtual shard",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).Build()
			},
			want: sqlcstore.ErrMissingVShard,
		},
		{
			name: "duplicate virtual shard",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).
					Link(0, replicaSet).Link(0, replicaSet).Build()
			},
			want: sqlcstore.ErrDuplicateVShard,
		},
		{
			name: "link out of range",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).Link(1, replicaSet).Build()
			},
			want: sqlcstore.ErrVShardOutOfRange,
		},
		{
			name: "empty replica set name",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).
					Link(0, sqlcstore.NewReplicaSet("", node("main"), nil)).Build()
			},
			want: sqlcstore.ErrEmptyReplicaSetName,
		},
		{
			name: "nil replica set",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](1).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).Link(0, nil).Build()
			},
			want: sqlcstore.ErrNilReplicaSet,
		},
		{
			name: "duplicate physical name",
			make: func() (*sqlcstore.Mesh[string, *fakeWriter, uint64], error) {
				return sqlcstore.NewBuilder[string, *fakeWriter, uint64](2).
					WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).
					Link(0, sqlcstore.NewReplicaSet("same", node("a"), nil)).
					Link(1, sqlcstore.NewReplicaSet("same", node("b"), nil)).Build()
			},
			want: sqlcstore.ErrDuplicateReplicaSet,
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
	mesh, err := sqlcstore.CreateMesh(t.Context(), &sqlcstore.Options[string, *fakeWriter, uint64]{
		ReplicaSets: []sqlcstore.ReplicaSetSpec{
			{Name: "a", Primary: sqlcstore.Connection{DSN: "a-primary"}, Replicas: []sqlcstore.Connection{{DSN: "a-replica"}}},
			{Name: "b", Primary: sqlcstore.Connection{DSN: "b-primary"}},
		},
		Shards: sqlcstore.Shards{
			NumVShards: 2,
			Mappings: []sqlcstore.VShardMapping{
				{VShards: []uint64{0}, MainReplicaSet: "a", MirrorReplicaSets: []string{"b"}},
				{VShards: []uint64{1}, MainReplicaSet: "b"},
			},
		},
		CreateNode: func(_ context.Context, dsn string) (sqlcstore.Node[string, *fakeWriter], error) {
			created = append(created, dsn)
			return node(dsn), nil
		},
		ShardHasher: sqlcstore.ModularShardHashFor[uint64](2),
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
	_, err := sqlcstore.CreateMesh[string, *fakeWriter, uint64](t.Context(), nil)
	require.ErrorIs(t, err, sqlcstore.ErrNoReplicaSets)

	valid := func() *sqlcstore.Options[string, *fakeWriter, uint64] {
		return &sqlcstore.Options[string, *fakeWriter, uint64]{
			ReplicaSets: []sqlcstore.ReplicaSetSpec{{
				Name:    "main",
				Primary: sqlcstore.Connection{DSN: "primary"},
			}},
			Shards: sqlcstore.Shards{
				NumVShards: 1,
				Mappings:   []sqlcstore.VShardMapping{{VShards: []uint64{0}, MainReplicaSet: "main"}},
			},
			CreateNode: func(context.Context, string) (sqlcstore.Node[string, *fakeWriter], error) {
				return node("main"), nil
			},
			ShardHasher: sqlcstore.ConstantShardHashFor[uint64](0),
		}
	}

	tests := []struct {
		name string
		edit func(*sqlcstore.Options[string, *fakeWriter, uint64])
		want error
	}{
		{
			name: "no replica sets",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ReplicaSets = nil },
			want: sqlcstore.ErrNoReplicaSets,
		},
		{
			name: "empty name",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Name = "" },
			want: sqlcstore.ErrEmptyReplicaSetName,
		},
		{
			name: "whitespace name",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Name = " \t" },
			want: sqlcstore.ErrEmptyReplicaSetName,
		},
		{name: "duplicate name", edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
			o.ReplicaSets = append(o.ReplicaSets, o.ReplicaSets[0])
		}, want: sqlcstore.ErrDuplicateReplicaSet},
		{
			name: "empty DSN",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Primary.DSN = "" },
			want: sqlcstore.ErrEmptyDSN,
		},
		{
			name: "whitespace primary DSN",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ReplicaSets[0].Primary.DSN = " \t" },
			want: sqlcstore.ErrEmptyDSN,
		},
		{
			name: "empty replica DSN",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
				o.ReplicaSets[0].Replicas = []sqlcstore.Connection{{DSN: ""}}
			},
			want: sqlcstore.ErrEmptyDSN,
		},
		{
			name: "no factory",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.CreateNode = nil },
			want: sqlcstore.ErrNoNodeFactory,
		},
		{
			name: "no hasher",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.ShardHasher = nil },
			want: sqlcstore.ErrNoShardHasher,
		},
		{
			name: "no virtual shards",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.Shards.NumVShards = 0 },
			want: sqlcstore.ErrNoVShards,
		},
		{name: "unknown main", edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings[0].MainReplicaSet = "missing"
		}, want: sqlcstore.ErrUnknownReplicaSet},
		{name: "unknown mirror", edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings[0].MirrorReplicaSets = []string{"missing"}
		}, want: sqlcstore.ErrUnknownReplicaSet},
		{
			name: "self mirror",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
				o.Shards.Mappings[0].MirrorReplicaSets = []string{"main"}
			},
			want: sqlcstore.ErrMirrorConfiguration,
		},
		{
			name: "duplicate mirror",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
				o.ReplicaSets = append(
					o.ReplicaSets,
					sqlcstore.ReplicaSetSpec{Name: "mirror", Primary: sqlcstore.Connection{DSN: "mirror"}},
				)
				o.Shards.Mappings[0].MirrorReplicaSets = []string{"mirror", "mirror"}
			},
			want: sqlcstore.ErrMirrorConfiguration,
		},
		{
			name: "missing vshard",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.Shards.Mappings = nil },
			want: sqlcstore.ErrMissingVShard,
		},
		{name: "duplicate vshard", edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
			o.Shards.Mappings = append(o.Shards.Mappings, o.Shards.Mappings[0])
		}, want: sqlcstore.ErrDuplicateVShard},
		{
			name: "out of range",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) { o.Shards.Mappings[0].VShards = []uint64{1} },
			want: sqlcstore.ErrVShardOutOfRange,
		},
		{
			name: "inconsistent mirrors",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
				o.Shards.NumVShards = 2
				o.ReplicaSets = append(
					o.ReplicaSets,
					sqlcstore.ReplicaSetSpec{Name: "mirror", Primary: sqlcstore.Connection{DSN: "mirror"}},
				)
				o.Shards.Mappings = []sqlcstore.VShardMapping{
					{VShards: []uint64{0}, MainReplicaSet: "main"},
					{VShards: []uint64{1}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror"}},
				}
			},
			want: sqlcstore.ErrMirrorConfiguration,
		},
		{
			name: "inconsistent mirror order",
			edit: func(o *sqlcstore.Options[string, *fakeWriter, uint64]) {
				o.Shards.NumVShards = 2
				o.ReplicaSets = append(
					o.ReplicaSets,
					sqlcstore.ReplicaSetSpec{Name: "mirror-a", Primary: sqlcstore.Connection{DSN: "mirror-a"}},
					sqlcstore.ReplicaSetSpec{Name: "mirror-b", Primary: sqlcstore.Connection{DSN: "mirror-b"}},
				)
				o.Shards.Mappings = []sqlcstore.VShardMapping{
					{VShards: []uint64{0}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror-a", "mirror-b"}},
					{VShards: []uint64{1}, MainReplicaSet: "main", MirrorReplicaSets: []string{"mirror-b", "mirror-a"}},
				}
			},
			want: sqlcstore.ErrMirrorConfiguration,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts := valid()
			test.edit(opts)
			_, err := sqlcstore.CreateMesh(t.Context(), opts)
			assert.ErrorIs(t, err, test.want)
		})
	}
}

func TestCreateMeshWrapsFactoryError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("connect failed")
	_, err := sqlcstore.CreateMesh(t.Context(), &sqlcstore.Options[string, *fakeWriter, uint64]{
		ReplicaSets: []sqlcstore.ReplicaSetSpec{{Name: "main", Primary: sqlcstore.Connection{DSN: "primary"}}},
		Shards: sqlcstore.Shards{NumVShards: 1, Mappings: []sqlcstore.VShardMapping{{
			VShards: []uint64{0}, MainReplicaSet: "main",
		}}},
		CreateNode: func(context.Context, string) (sqlcstore.Node[string, *fakeWriter], error) {
			return sqlcstore.Node[string, *fakeWriter]{}, sentinel
		},
		ShardHasher: sqlcstore.ConstantShardHashFor[uint64](0),
	})
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), fmt.Sprintf("primary node for replica set %q", "main"))
}
