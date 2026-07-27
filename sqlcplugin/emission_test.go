package sqlcplugin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateUsesArrayOverridesWithoutLeakingStructImports(t *testing.T) {
	t.Parallel()

	xidColumnType := &plugin.Identifier{Schema: "public", Name: "xid"}
	timestampType := &plugin.Identifier{Name: "timestamptz"}
	messageTable := &plugin.Identifier{Schema: "public", Name: "message"}

	resp, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{
			Engine: "postgresql",
			Codegen: &plugin.Codegen{
				Out: "internal",
			},
		},
		Catalog: &plugin.Catalog{
			DefaultSchema: "public",
			Schemas: []*plugin.Schema{{
				Name: "public",
				Tables: []*plugin.Table{{
					Rel: messageTable,
					Columns: []*plugin.Column{
						{Name: "id", NotNull: true, Type: xidColumnType},
						{Name: "created_at", NotNull: true, Type: timestampType},
					},
				}},
			}},
		},
		Queries: []*plugin.Query{{
			Name:     "ListMessagesWithIDs",
			Cmd:      ":many",
			Comments: []string{"kind: read"},
			Text:     "SELECT * FROM message WHERE id = ANY(@ids::public.xid[])",
			Params: []*plugin.Parameter{{
				Number: 1,
				Column: &plugin.Column{
					Name:        "ids",
					Type:        xidColumnType,
					NotNull:     true,
					IsSqlcSlice: true,
				},
			}},
			Columns: []*plugin.Column{
				{Name: "id", NotNull: true, Type: xidColumnType, Table: messageTable},
				{Name: "created_at", NotNull: true, Type: timestampType, Table: messageTable},
			},
		}, {
			Name:     "CreateMessage",
			Cmd:      ":one",
			Comments: []string{"kind: write", "CreateMessage can keep normal comments after the kind annotation."},
			Text:     "INSERT INTO message (id, created_at) VALUES ($1, $2) RETURNING *",
			Params: []*plugin.Parameter{{
				Number: 1,
				Column: &plugin.Column{
					Name:    "id",
					Type:    xidColumnType,
					NotNull: true,
				},
			}, {
				Number: 2,
				Column: &plugin.Column{
					Name:    "created_at",
					Type:    timestampType,
					NotNull: true,
				},
			}},
			Columns: []*plugin.Column{
				{Name: "id", NotNull: true, Type: xidColumnType, Table: messageTable},
				{Name: "created_at", NotNull: true, Type: timestampType, Table: messageTable},
			},
		}},
		PluginOptions: []byte(`{
			"package": "internal",
			"sql_package": "pgx/v5",
			"emit_params_struct_pointers": true,
			"emit_result_struct_pointers": true,
			"overrides": [
				{
					"db_type": "public.xid",
					"go_type": {
						"import": "github.com/sundayfun/siu/toolkit/xid",
						"type": "ID"
					}
				}
			]
		}`),
	})
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{
			"store_querier_interfaces.go",
			"store_querier_read.go",
			"store_querier_write.go",
			"store_querier.go",
			"store_querier_sharded.go",
		},
		generatedFileNames(resp),
	)
	interfaces := generatedFileContents(t, resp, "store_querier_interfaces.go")
	assert.Contains(t, interfaces, "type readQuerier interface")
	assert.Contains(t, interfaces, "type writeQuerier interface")
	assert.Contains(t, interfaces, "type Store interface")
	assert.NotContains(t, interfaces, "type readQueries struct")
	assert.NotContains(t, interfaces, "type writeQueries struct")

	read := generatedFileContents(t, resp, "store_querier_read.go")
	assert.Contains(t, read, "type readQueries struct")
	assert.NotContains(t, read, "type writeQueries struct")

	write := generatedFileContents(t, resp, "store_querier_write.go")
	assert.Contains(t, write, "type writeQueries struct")
	assert.NotContains(t, write, "type readQueries struct")

	store := generatedFileContents(t, resp, "store_querier.go")
	assert.Contains(t, store, "type meshStore[SK any] struct")
	assert.NotContains(t, store, "defaultShardKey")
	assert.Contains(t, store, "func NewStore(ctx context.Context, topology Topology, options ...StoreOption) (Store, error)")
	assert.Contains(t, store, "func Singleton(primary DBTX, options ...SingletonOption) Topology")
	assert.Contains(t, store, "// WithDatabaseName identifies the database in telemetry.")
	assert.Contains(t, store, "// WithReadReplicas appends databases used for round-robin reads.")
	assert.Contains(t, store, "// WithWriteMirrors appends databases that synchronously receive writes.")
	assert.NotContains(t, store, "type OneStore")
	assert.NotContains(t, store, "type ShardedStore")
	assert.NotContains(t, generatedSource(resp), "oneStore")

	sharded := generatedFileContents(t, resp, "store_querier_sharded.go")
	assert.NotContains(t, sharded, "type ShardedConfig")
	assert.NotContains(t, sharded, "type ShardDatabaseConfig")

	got := generatedSource(resp)
	assert.Contains(
		t,
		got,
		"type readQuerier interface {\n\t// ListMessagesWithIDs executes the generated ListMessagesWithIDs query.\n"+
			"\tListMessagesWithIDs(ctx context.Context, ids []xid.ID) ([]*Message, error)\n}",
	)
	assert.Contains(
		t,
		got,
		"type writeQuerier interface {\n\t// CreateMessage executes the generated CreateMessage query.\n"+
			"\tCreateMessage(ctx context.Context, arg *CreateMessageParams) (*Message, error)\n}",
	)
	assert.Contains(t, got, "type Store interface")
	assert.Contains(t, got, "type queryStore struct {\n\t*readQueries\n\t*writeQueries\n}")
	assert.Contains(t, got, "var _ readQuerier = (*readQueries)(nil)")
	assert.Contains(t, got, "var _ writeQuerier = (*writeQueries)(nil)")
	readBody := generatedMethodBody(t, got, "readQueries", "ListMessagesWithIDs")
	assert.NotContains(t, readBody, ".mirror(")
	assert.NotContains(t, readBody, "mirror.ListMessagesWithIDs")
	assert.Contains(t, readBody, "return rv0, nil")
	writeBody := generatedMethodBody(t, got, "writeQueries", "CreateMessage")
	assert.Contains(t, writeBody, "mirror.CreateMessage")
	assert.NotContains(t, got, `"time"`)
}

func generatedMethodBody(t *testing.T, source, receiverType, methodName string) string {
	t.Helper()

	start := strings.Index(source, "func (q *"+receiverType+") "+methodName+"(")
	require.NotEqual(t, -1, start, "generated output missing %s.%s method", receiverType, methodName)
	rest := source[start:]
	end := strings.Index(rest, "\n}\n\n")
	if end == -1 {
		end = strings.Index(rest, "\n}\n")
	}
	require.NotEqual(t, -1, end, "generated output missing end of %s method", methodName)
	return rest[:end+3]
}

func generatedSource(response *plugin.GenerateResponse) string {
	var source strings.Builder
	for _, file := range response.GetFiles() {
		source.Write(file.GetContents())
		source.WriteString("\n")
	}
	return source.String()
}

func generatedFileNames(response *plugin.GenerateResponse) []string {
	names := make([]string, 0, len(response.GetFiles()))
	for _, file := range response.GetFiles() {
		names = append(names, file.GetName())
	}
	return names
}

func generatedFileContents(t *testing.T, response *plugin.GenerateResponse, name string) string {
	t.Helper()
	for _, file := range response.GetFiles() {
		if file.GetName() == name {
			return string(file.GetContents())
		}
	}
	require.Failf(t, "generated response missing file", "missing %s", name)
	return ""
}

func TestNamedResultsSignatureAvoidsParameterAndReceiverNames(t *testing.T) {
	t.Parallel()

	signature, names, errName := namedResultsSignature(
		[]argument{{name: "result"}, {name: "err"}},
		[]string{"int64", "error"},
		"result2",
	)

	assert.Equal(t, " (result3 int64, err2 error)", signature)
	assert.Equal(t, []string{"result3", "err2"}, names)
	assert.Equal(t, "err2", errName)
}

func TestOutputPackageName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want string
	}{
		{name: "directory basename", out: "generated/internal", want: "internal"},
		{name: "current directory fallback", out: ".", want: "db"},
		{name: "invalid identifier fallback", out: "generated-db", want: "db"},
		{name: "keyword fallback", out: "type", want: "db"},
		{name: "empty fallback", want: "db"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := &plugin.GenerateRequest{Settings: &plugin.Settings{Codegen: &plugin.Codegen{Out: test.out}}}
			assert.Equal(t, test.want, outputPackageName(request))
		})
	}
}

func TestClassifyQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     *plugin.Query
		want      queryKind
		wantRoute *routeAnnotation
		wantErr   string
	}{
		{
			name: "select",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read"},
			},
			want: queryKindRead,
		},
		{
			name: "shard route",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "shard: p2p(user_id, peer_id)", "documentation"},
			},
			want:      queryKindRead,
			wantRoute: &routeAnnotation{name: "p2p", operands: []string{"user_id", "peer_id"}},
		},
		{
			name: "shard route without operands",
			query: &plugin.Query{
				Name:     "GetGlobalSetting",
				Comments: []string{"kind: read", "shard: global()"},
			},
			want:      queryKindRead,
			wantRoute: &routeAnnotation{name: "global", operands: nil},
		},
		{
			name: "insert returning",
			query: &plugin.Query{
				Name:     "CreateMessage",
				Comments: []string{"kind: write"},
				Text:     "INSERT INTO message (id) VALUES ($1) RETURNING *",
			},
			want: queryKindWrite,
		},
		{
			name: "allows comments after annotation",
			query: &plugin.Query{
				Name:     "UpdateMessage",
				Comments: []string{"kind: write", "normal comment"},
			},
			want: queryKindWrite,
		},
		{
			name: "falls back to leading sql comment",
			query: &plugin.Query{
				Name: "CreateMessage",
				Text: "-- name: CreateMessage :one\n-- kind: write\nINSERT INTO message (id) VALUES ($1) RETURNING *",
			},
			want: queryKindWrite,
		},
		{
			name: "kind annotation must be adjacent to sqlc name",
			query: &plugin.Query{
				Name: "CreateMessage",
				Text: "-- name: CreateMessage :one\n\n-- kind: write\nINSERT INTO message (id) VALUES ($1) RETURNING *",
			},
			wantErr: "kind annotation must immediately follow",
		},
		{
			name: "shard annotation must be adjacent to kind",
			query: &plugin.Query{
				Name: "ListMessages",
				Text: "-- name: ListMessages :many\n-- kind: read\n\n-- shard: inbox(user_id)\nSELECT 1",
			},
			wantErr: "shard annotation must immediately follow",
		},
		{
			name: "shard annotation must be second",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "documentation", "shard: p2p(user_id, peer_id)"},
			},
			wantErr: "must immediately follow",
		},
		{
			name: "malformed shard annotation",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "shard: user_id"},
			},
			wantErr: "malformed shard annotation",
		},
		{
			name: "invalid shard route name",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "shard: 1route(user_id)"},
			},
			wantErr: "invalid shard route name",
		},
		{
			name: "invalid shard operand",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "shard: inbox(1user_id)"},
			},
			wantErr: "invalid shard operand",
		},
		{
			name: "duplicate shard operand",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: read", "shard: inbox(user_id, user_id)"},
			},
			wantErr: "repeats shard operand",
		},
		{
			name: "missing annotation",
			query: &plugin.Query{
				Name: "ListMessages",
				Text: "SELECT * FROM message",
			},
			wantErr: "missing required kind annotation",
		},
		{
			name: "annotation must be first comment",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"normal comment", "kind: read"},
			},
			wantErr: "first comment must be kind annotation",
		},
		{
			name: "invalid annotation",
			query: &plugin.Query{
				Name:     "ListMessages",
				Comments: []string{"kind: maybe"},
			},
			wantErr: "invalid kind annotation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, route, err := classifyQuery(tt.query)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantRoute, route)
		})
	}
}

func TestGenerateShardRoutedFacade(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	request := &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		Queries: []*plugin.Query{
			{
				Name:     "ListP2PMessages",
				Cmd:      ":many",
				Comments: []string{"kind: read", "shard: p2p(user_id, peer_id)"},
				Params: []*plugin.Parameter{
					{Number: 1, Column: &plugin.Column{Name: "user_id", Type: int8Type, NotNull: true}},
					{Number: 2, Column: &plugin.Column{Name: "peer_id", Type: int8Type, NotNull: true}},
				},
				Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			},
			{
				Name:     "CreateP2PMessage",
				Cmd:      ":one",
				Comments: []string{"kind: write", "shard: p2p(user_id, peer_id)"},
				Params: []*plugin.Parameter{
					{Number: 1, Column: &plugin.Column{Name: "user_id", Type: int8Type, NotNull: true}},
					{Number: 2, Column: &plugin.Column{Name: "peer_id", Type: int8Type, NotNull: true}},
				},
				Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			},
		},
		PluginOptions: []byte(`{
			"package":"db",
			"sql_package":"pgx/v5",
			"query_parameter_limit":1,
			"emit_params_struct_pointers":true
		}`),
	}

	response, err := Generate(t.Context(), request)
	require.NoError(t, err)
	got := generatedSource(response)
	checks := []string{
		`func NewStore(ctx context.Context, topology Topology, options ...StoreOption) (Store, error)`,
		`func Singleton(primary DBTX, options ...SingletonOption) Topology`,
		`func Sharded[SK any](numVShards uint64, shardHasher pgmesh.ShardHasher[SK], resolver ShardResolver[SK], options ...ShardedOption) Topology`,
		`func WithReplicaSet(name string, primary DBTX, replicas ...DBTX) ShardedOption`,
		`func WithVShardMapping(mainReplicaSet string, vshards []uint64, mirrorReplicaSets ...string) ShardedOption`,
		`func newStoreNode(database DBTX) pgmesh.Node[*readQueries, *queryStore]`,
		"type ShardResolver[SK any] interface {\n\t// P2P resolves the \"p2p\" shard route.\n" +
			"\tP2P(userID int64, peerID int64) SK\n}",
		"type meshStore[SK any] struct",
		"type Store interface",
		"func ReadFromPrimary() QueryOption",
		"func WithTx(tx pgx.Tx) QueryOption",
		"func (q *meshStore[SK]) ListP2PMessages(ctx context.Context, arg *ListP2PMessagesParams, storeOptions ...QueryOption) (result []int64, err error)",
		"var shardKey SK",
		"shardKey = q.resolver.P2P(arg.UserID, arg.PeerID)",
		`q.mesh.StartSpan(ctx, "Store", "ListP2PMessages", pgmesh.QueryKindRead)`,
		`q.mesh.StartSpan(ctx, "Store", "CreateP2PMessage", pgmesh.QueryKindWrite)`,
		"// Trace the query and record its returned error.",
		"defer func() { querySpan.End(err) }()",
		"// Resolve the shard key for this topology.",
		"// Apply options that can override the default route.",
		"querySpan.SetRoute(shard.VShardIndex(), shard.Name(), pgmesh.RouteModeRead, 0)",
		"querySpan.SetRoute(shard.VShardIndex(), shard.Name(), pgmesh.RouteModeTransaction, 0)",
		"return shard.Read().ListP2PMessages(ctx, arg)",
		"return shard.Write().WithTx(options.tx).ListP2PMessages(ctx, arg)",
		"target := shard.Write()",
		"writeMirrorCount := shard.WriteMirrorCount()",
		"querySpan.SetRoute(shard.VShardIndex(), shard.Name(), mode, writeMirrorCount)",
		"return target.CreateP2PMessage(ctx, arg)",
	}
	for _, check := range checks {
		assert.Contains(t, got, check)
	}
	meshReadBody := generatedMethodBody(t, got, "meshStore[SK]", "ListP2PMessages")
	assert.NotContains(t, meshReadBody, "var queryErr error")
	assert.NotContains(t, meshReadBody, "queryErr =")
	assert.Equal(t, 1, strings.Count(got, "type meshStore[SK any] struct"))
	assert.Equal(t, 1, strings.Count(got, "func (q *meshStore[SK]) ListP2PMessages("))
	assert.NotContains(t, got, "defaultShardKey")
	assert.NotContains(t, got, "type databaseStore struct")
	assert.NotContains(t, got, "type shardedStore[SK any] struct")
	assert.Contains(t, got, "func (q *queryStore) WithTx(tx pgx.Tx) *queryStore")
	assert.Contains(t, got, "return newQueryStore(q.writeQueries.main.WithTx(tx))")
}

func TestGenerateRejectsMixedShardedAndUnshardedQueries(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	_, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		Queries: []*plugin.Query{
			{
				Name:     "GetUser",
				Cmd:      ":one",
				Comments: []string{"kind: read", "shard: tenant(tenant_id)"},
				Params: []*plugin.Parameter{{
					Number: 1,
					Column: &plugin.Column{Name: "tenant_id", Type: int8Type, NotNull: true},
				}},
				Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			},
			{
				Name:     "ListUsers",
				Cmd:      ":many",
				Comments: []string{"kind: read"},
				Columns:  []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			},
		},
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5"}`),
	})
	require.ErrorContains(t, err, "query ListUsers must declare shard metadata")
	require.ErrorContains(t, err, "move unsharded queries to another generated store")
}

func TestGenerateResolvesShardOperandsForIndividualParameters(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		Queries: []*plugin.Query{{
			Name:     "GetP2PMessage",
			Cmd:      ":one",
			Comments: []string{"kind: read", "shard: p2p(user_id, peer_id)"},
			Params: []*plugin.Parameter{
				{Number: 1, Column: &plugin.Column{Name: "user_id", Type: int8Type, NotNull: true}},
				{Number: 2, Column: &plugin.Column{Name: "peer_id", Type: int8Type, NotNull: true}},
			},
			Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
		}},
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5","query_parameter_limit":2}`),
	})
	require.NoError(t, err)
	got := generatedSource(response)
	assert.Contains(t, got, "shardKey = q.resolver.P2P(userID, peerID)")
}

func TestGenerateIgnoreMirrorErrorOption(t *testing.T) {
	t.Parallel()

	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		Queries: []*plugin.Query{{
			Name:     "DeleteUser",
			Cmd:      ":exec",
			Comments: []string{"kind: write"},
		}},
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5","ignore_mirror_error":true}`),
	})
	require.NoError(t, err)

	got := generatedSource(response)
	mirrorBody := generatedMethodBody(t, got, "writeQueries", "mirror")
	assert.Contains(t, mirrorBody, "if err := fn(mirror); err != nil {\n\t\t\tcontinue")
	assert.NotContains(t, mirrorBody, "return err")
	assert.NotContains(t, got, `"database/sql"`)
	assert.NotContains(t, got, `"errors"`)
}

func TestGenerateEmptyQuerySetStillEmitsStoreConfiguration(t *testing.T) {
	t.Parallel()

	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings:      &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:       &plugin.Catalog{DefaultSchema: "public"},
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5"}`),
	})
	require.NoError(t, err)
	assert.Contains(
		t,
		generatedSource(response),
		"func NewStore(ctx context.Context, topology Topology, options ...StoreOption) (Store, error)",
	)
}

func TestGenerateRejectsInvalidRoutingConfigurations(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	textType := &plugin.Identifier{Name: "text"}
	base := func(queries ...*plugin.Query) *plugin.GenerateRequest {
		return &plugin.GenerateRequest{
			Settings:      &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
			Catalog:       &plugin.Catalog{DefaultSchema: "public"},
			Queries:       queries,
			PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5"}`),
		}
	}

	tests := []struct {
		name    string
		request *plugin.GenerateRequest
		want    string
	}{
		{
			name: "unknown operand",
			request: base(&plugin.Query{
				Name: "GetMessage", Cmd: ":one", Comments: []string{"kind: read", "shard: inbox(missing_id)"},
				Params:  []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "inbox_id", Type: int8Type, NotNull: true}}},
				Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			}),
			want: "does not match a SQL parameter",
		},
		{
			name: "route rename must remain exported",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{
					Name: "GetMessage", Cmd: ":one", Comments: []string{"kind: read", "shard: inbox(inbox_id)"},
					Params:  []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "inbox_id", Type: int8Type, NotNull: true}}},
					Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
				})
				r.PluginOptions = []byte(`{"package":"db","sql_package":"pgx/v5","rename":{"inbox":"privateRoute"}}`)
				return r
			}(),
			want: "non-exported or invalid resolver method",
		},
		{
			name: "copyfrom route",
			request: base(&plugin.Query{
				Name: "CreateMessages", Cmd: ":copyfrom", Comments: []string{"kind: write", "shard: inbox(inbox_id)"},
				Params: []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "inbox_id", Type: int8Type, NotNull: true}}},
			}),
			want: "cannot declare shard metadata",
		},
		{
			name: "batch route",
			request: base(&plugin.Query{
				Name: "GetMessages", Cmd: ":batchmany", Comments: []string{"kind: read", "shard: inbox(inbox_id)"},
				Params:  []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "inbox_id", Type: int8Type, NotNull: true}}},
				Columns: []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			}),
			want: "cannot declare shard metadata",
		},
		{
			name: "conflicting route signatures",
			request: base(
				&plugin.Query{
					Name:     "GetByID",
					Cmd:      ":one",
					Comments: []string{"kind: read", "shard: entity(id)"},
					Params:   []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "id", Type: int8Type, NotNull: true}}},
					Columns:  []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
				},
				&plugin.Query{
					Name:     "GetByName",
					Cmd:      ":one",
					Comments: []string{"kind: read", "shard: entity(name)"},
					Params:   []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "name", Type: textType, NotNull: true}}},
					Columns:  []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
				},
			),
			want: "incompatible parameter types",
		},
		{
			name: "non pgx driver",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{"package":"db","sql_package":"database/sql"}`)
				return r
			}(),
			want: "requires pgx/v5",
		},
		{
			name: "non postgres engine",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.Settings.Engine = "mysql"
				return r
			}(),
			want: "requires postgresql",
		},
		{
			name: "skip transaction support",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{"package":"db","sql_package":"pgx/v5","skip_with_tx":true}`)
				return r
			}(),
			want: `unknown field "skip_with_tx"`,
		},
		{
			name: "negative parameter limit",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{"package":"db","sql_package":"pgx/v5","query_parameter_limit":-1}`)
				return r
			}(),
			want: "must not be negative",
		},
		{
			name: "malformed options",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{`)
				return r
			}(),
			want: "unmarshal plugin options",
		},
		{
			name: "invalid string override type",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{
					"package":"db",
					"sql_package":"pgx/v5",
					"overrides":[{"db_type":"text","go_type":"LocalType"}]
				}`)
				return r
			}(),
			want: "is not a Go basic type",
		},
		{
			name: "override package without import",
			request: func() *plugin.GenerateRequest {
				r := base(&plugin.Query{Name: "Delete", Cmd: ":exec", Comments: []string{"kind: write"}})
				r.PluginOptions = []byte(`{
					"package":"db",
					"sql_package":"pgx/v5",
					"overrides":[{"db_type":"text","go_type":{"package":"custom","type":"Value"}}]
				}`)
				return r
			}(),
			want: "package requires an import path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Generate(t.Context(), test.request)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestGenerateSupportsAllNodeLevelCommands(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	tests := []struct {
		name      string
		command   string
		signature string
		body      []string
	}{
		{
			name:      "one",
			command:   ":one",
			signature: "Query0(ctx context.Context) (int64, error)",
			body:      []string{"q.main.Query0(ctx)", "return rv0, nil"},
		},
		{
			name:      "many",
			command:   ":many",
			signature: "Query1(ctx context.Context) ([]int64, error)",
			body:      []string{"q.main.Query1(ctx)", "return rv0, nil"},
		},
		{
			name:      "exec",
			command:   ":exec",
			signature: "Query2(ctx context.Context) error",
			body:      []string{"q.main.Query2(ctx)", "return nil"},
		},
		{
			name:      "exec rows",
			command:   ":execrows",
			signature: "Query3(ctx context.Context) (int64, error)",
			body:      []string{"q.main.Query3(ctx)", "return rv0, nil"},
		},
		{
			name:      "exec result",
			command:   ":execresult",
			signature: "Query4(ctx context.Context) (pgconn.CommandTag, error)",
			body:      []string{"q.main.Query4(ctx)", "return rv0, nil"},
		},
		{
			name:      "copy from",
			command:   ":copyfrom",
			signature: "Query5(ctx context.Context, id []int64) (int64, error)",
			body:      []string{"q.main.Query5(ctx, id)", "return rv0, nil"},
		},
		{
			name:      "batch exec",
			command:   ":batchexec",
			signature: "Query6(ctx context.Context, id []int64) *Query6BatchResults",
			body:      []string{"return q.main.Query6(ctx, id)"},
		},
		{
			name:      "batch one",
			command:   ":batchone",
			signature: "Query7(ctx context.Context, id []int64) *Query7BatchResults",
			body:      []string{"return q.main.Query7(ctx, id)"},
		},
		{
			name:      "batch many",
			command:   ":batchmany",
			signature: "Query8(ctx context.Context, id []int64) *Query8BatchResults",
			body:      []string{"return q.main.Query8(ctx, id)"},
		},
	}
	queries := make([]*plugin.Query, 0, len(tests))
	for index, test := range tests {
		query := &plugin.Query{
			Name:     fmt.Sprintf("Query%d", index),
			Cmd:      test.command,
			Comments: []string{"kind: read"},
		}
		if test.command == ":one" || test.command == ":many" || test.command == ":batchone" || test.command == ":batchmany" {
			query.Columns = []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}}
		}
		if test.command == ":copyfrom" || strings.HasPrefix(test.command, ":batch") {
			query.Params = []*plugin.Parameter{{Number: 1, Column: &plugin.Column{Name: "id", Type: int8Type, NotNull: true}}}
		}
		queries = append(queries, query)
	}

	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings:      &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:       &plugin.Catalog{DefaultSchema: "public"},
		Queries:       queries,
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5"}`),
	})
	require.NoError(t, err)
	got := generatedSource(response)
	for index, test := range tests {
		assert.Contains(t, got, test.signature, "command %s", test.command)
		body := generatedMethodBody(t, got, "readQueries", fmt.Sprintf("Query%d", index))
		for _, want := range test.body {
			assert.Contains(t, body, want, "command %s body", test.command)
		}
	}
}

func TestGenerateQualifiesSqlcTypesForSeparatePackage(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	tokenType := &plugin.Identifier{Schema: "public", Name: "token"}
	users := &plugin.Identifier{Schema: "public", Name: "users"}
	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "store"}},
		Catalog: &plugin.Catalog{
			DefaultSchema: "public",
			Schemas: []*plugin.Schema{{
				Name: "public",
				Tables: []*plugin.Table{{
					Rel: users,
					Columns: []*plugin.Column{
						{Name: "id", Type: int8Type, NotNull: true},
						{Name: "tenant_id", Type: int8Type, NotNull: true},
						{Name: "token", Type: tokenType, NotNull: true},
					},
				}},
			}},
		},
		Queries: []*plugin.Query{{
			Name:     "GetUser",
			Cmd:      ":one",
			Comments: []string{"kind: read", "shard: user(token)"},
			Params: []*plugin.Parameter{
				{Number: 1, Column: &plugin.Column{Name: "id", Type: int8Type, NotNull: true}},
				{Number: 2, Column: &plugin.Column{Name: "tenant_id", Type: int8Type, NotNull: true}},
				{Number: 3, Column: &plugin.Column{Name: "token", Type: tokenType, NotNull: true}},
			},
			Columns: []*plugin.Column{
				{Name: "id", Type: int8Type, NotNull: true, Table: users},
				{Name: "tenant_id", Type: int8Type, NotNull: true, Table: users},
				{Name: "token", Type: tokenType, NotNull: true, Table: users},
			},
		}},
		PluginOptions: []byte(`{
			"package":"store",
			"output_file_name":"generated_store.go",
			"internal_import_path":"example.test/project/internal/db",
			"internal_import_alias":"db",
			"runtime_import_path":"example.test/project/pgmesh",
			"sql_package":"pgx/v5",
			"query_parameter_limit":1,
			"emit_params_struct_pointers":true,
			"emit_result_struct_pointers":true,
			"overrides":[{"db_type":"public.token","go_type":{"type":"Token"}}]
		}`),
	})
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{
			"generated_store_interfaces.go",
			"generated_store_read.go",
			"generated_store_write.go",
			"generated_store.go",
			"generated_store_sharded.go",
		},
		generatedFileNames(response),
	)

	got := generatedSource(response)
	checks := []string{
		`db "example.test/project/internal/db"`,
		`pgmesh "example.test/project/pgmesh"`,
		"GetUser(ctx context.Context, arg *db.GetUserParams) (*db.User, error)",
		"GetUser(ctx context.Context, arg *db.GetUserParams, storeOptions ...QueryOption) (*db.User, error)",
		"User(token db.Token) SK",
		"main *db.Queries",
		"func newReadQueries(q *db.Queries) *readQueries",
		"var _ db.Querier = (*queryStore)(nil)",
		"queries := db.New(database)",
	}
	for _, check := range checks {
		assert.Contains(t, got, check)
	}
}

func TestGenerateAppliesRenameAndNullableOptions(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	textType := &plugin.Identifier{Name: "text"}
	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		Queries: []*plugin.Query{{
			Name:     "FindUser",
			Cmd:      ":one",
			Comments: []string{"kind: read", "shard: tenant(tenant_id)"},
			Params: []*plugin.Parameter{
				{Number: 1, Column: &plugin.Column{Name: "tenant_id", Type: int8Type, NotNull: true}},
				{Number: 2, Column: &plugin.Column{Name: "display_name", Type: textType}},
			},
			Columns: []*plugin.Column{{Name: "display_name", Type: textType}},
		}},
		PluginOptions: []byte(`{
			"package":"db",
			"sql_package":"pgx/v5",
			"query_parameter_limit":1,
			"emit_params_struct_pointers":true,
			"emit_pointers_for_null_types":true,
			"rename":{"tenant":"ResolveTenant","tenant_id":"AccountID","display_name":"Label"}
		}`),
	})
	require.NoError(t, err)

	got := generatedSource(response)
	checks := []string{
		"FindUser(ctx context.Context, arg *FindUserParams) (*string, error)",
		"ResolveTenant(accountID int64) SK",
		"shardKey = q.resolver.ResolveTenant(arg.AccountID)",
	}
	for _, check := range checks {
		assert.Contains(t, got, check)
	}
}
