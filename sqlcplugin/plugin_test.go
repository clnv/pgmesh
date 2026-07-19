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
			"type": "StoreQueries",
			"constructor": "NewStoreQueries",
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
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(resp.GetFiles()) != 1 {
		t.Fatalf("expected one generated file, got %d", len(resp.GetFiles()))
	}

	got := string(resp.GetFiles()[0].GetContents())
	if !strings.Contains(
		got,
		"type ReadQuerier interface {\n\tListMessagesWithIDs(ctx context.Context, ids []xid.ID) ([]*Message, error)\n}",
	) {
		t.Fatalf("generated output did not put ListMessagesWithIDs in ReadQuerier:\n%s", got)
	}
	if !strings.Contains(
		got,
		"type WriteQuerier interface {\n\tCreateMessage(ctx context.Context, arg *CreateMessageParams) (*Message, error)\n}",
	) {
		t.Fatalf("generated output did not put CreateMessage in WriteQuerier:\n%s", got)
	}
	if !strings.Contains(got, "type StoreQuerier interface") {
		t.Fatalf("generated output did not include StoreQuerier:\n%s", got)
	}
	if !strings.Contains(got, "type StoreQueries struct {\n\t*ReadQueries\n\t*WriteQueries\n}") {
		t.Fatalf("generated output did not compose StoreQueries from ReadQueries and WriteQueries:\n%s", got)
	}
	if !strings.Contains(got, "var _ ReadQuerier = (*ReadQueries)(nil)") {
		t.Fatalf("generated output did not assert ReadQueries implements ReadQuerier:\n%s", got)
	}
	if !strings.Contains(got, "var _ WriteQuerier = (*WriteQueries)(nil)") {
		t.Fatalf("generated output did not assert WriteQueries implements WriteQuerier:\n%s", got)
	}
	readBody := generatedMethodBody(t, got, "ReadQueries", "ListMessagesWithIDs")
	if strings.Contains(readBody, ".mirror(") || strings.Contains(readBody, "mirror.ListMessagesWithIDs") {
		t.Fatalf("read query should not mirror:\n%s", readBody)
	}
	if !strings.Contains(readBody, "return rv0, nil") {
		t.Fatalf("read query should return main query result without mirror error:\n%s", readBody)
	}
	writeBody := generatedMethodBody(t, got, "WriteQueries", "CreateMessage")
	if !strings.Contains(writeBody, "mirror.CreateMessage") {
		t.Fatalf("write query should mirror:\n%s", writeBody)
	}
	if strings.Contains(got, `"time"`) {
		t.Fatalf("generated output imported time for hidden struct fields:\n%s", got)
	}
}

func generatedMethodBody(t *testing.T, source, receiverType, methodName string) string {
	t.Helper()

	start := strings.Index(source, "func (q *"+receiverType+") "+methodName+"(")
	if start == -1 {
		t.Fatalf("generated output missing %s.%s method:\n%s", receiverType, methodName, source)
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}\n\n")
	if end == -1 {
		end = strings.Index(rest, "\n}\n")
	}
	if end == -1 {
		t.Fatalf("generated output missing end of %s method:\n%s", methodName, rest)
	}
	return rest[:end+3]
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
				if err == nil {
					t.Fatalf("classifyQuery() returned nil error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("classifyQuery() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyQuery() returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("classifyQuery() = %s, want %s", got, tt.want)
			}
			if fmt.Sprintf("%#v", route) != fmt.Sprintf("%#v", tt.wantRoute) {
				t.Fatalf("classifyQuery() route = %#v, want %#v", route, tt.wantRoute)
			}
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
			{
				Name:     "ListGlobalMessages",
				Cmd:      ":many",
				Comments: []string{"kind: read"},
				Columns:  []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}},
			},
		},
		PluginOptions: []byte(`{
			"package":"db",
			"type":"StoreQueries",
			"constructor":"NewStoreQueries",
			"sql_package":"pgx/v5",
			"query_parameter_limit":1,
			"emit_params_struct_pointers":true
		}`),
	}

	response, err := Generate(t.Context(), request)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(response.GetFiles()[0].GetContents())
	checks := []string{
		`func NewStoreNode(database DBTX) sqlcstore.Node[*ReadQueries, *StoreQueries]`,
		"type ShardResolver[SK any] interface {\n\tP2P(userID int64, peerID int64) SK\n}",
		"type ShardedQueries[SK any] struct",
		"func ReadFromPrimary() RouteOption",
		"func WithTx(tx pgx.Tx) RouteOption",
		"shardKey := q.resolver.P2P(arg.UserID, arg.PeerID)",
		"return shard.Read().ListP2PMessages(ctx, arg)",
		"return shard.Write().WithTx(options.tx).ListP2PMessages(ctx, arg)",
		"target := shard.Write()",
		"return target.CreateP2PMessage(ctx, arg)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Fatalf("generated output missing %q:\n%s", check, got)
		}
	}
	if strings.Contains(got, "func (q *ShardedQueries[SK]) ListGlobalMessages") {
		t.Fatalf("unannotated query unexpectedly appeared in routed facade:\n%s", got)
	}
	if !strings.Contains(got, "func (q *ReadQueries) ListGlobalMessages") {
		t.Fatalf("unannotated query missing from node-level wrapper:\n%s", got)
	}
	if !strings.Contains(got, "func (q *StoreQueries) WithTx(tx pgx.Tx) *StoreQueries") ||
		!strings.Contains(got, "return newStoreQueries(q.WriteQueries.main.WithTx(tx))") {
		t.Fatalf("transaction wrapper must drop mirrors:\n%s", got)
	}
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
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(response.GetFiles()[0].GetContents())
	if !strings.Contains(got, "shardKey := q.resolver.P2P(userID, peerID)") {
		t.Fatalf("route did not reference individual parameters:\n%s", got)
	}
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

	got := string(response.GetFiles()[0].GetContents())
	mirrorBody := generatedMethodBody(t, got, "WriteQueries", "mirror")
	assert.Contains(t, mirrorBody, "if err := fn(mirror); err != nil {\n\t\t\tcontinue")
	assert.NotContains(t, mirrorBody, "return err")
	assert.NotContains(t, got, `"database/sql"`)
	assert.NotContains(t, got, `"errors"`)
}

func TestGenerateEmptyQuerySetDoesNotEmitUnusedContextImport(t *testing.T) {
	t.Parallel()

	response, err := Generate(t.Context(), &plugin.GenerateRequest{
		Settings:      &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:       &plugin.Catalog{DefaultSchema: "public"},
		PluginOptions: []byte(`{"package":"db","sql_package":"pgx/v5"}`),
	})
	require.NoError(t, err)
	assert.NotContains(t, string(response.GetFiles()[0].GetContents()), `"context"`)
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
			want: "skip_with_tx is unsupported",
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
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestGenerateSupportsAllNodeLevelCommands(t *testing.T) {
	t.Parallel()

	int8Type := &plugin.Identifier{Schema: "pg_catalog", Name: "int8"}
	commands := []string{":one", ":many", ":exec", ":execrows", ":execresult", ":copyfrom", ":batchexec", ":batchone", ":batchmany"}
	queries := make([]*plugin.Query, 0, len(commands))
	for index, command := range commands {
		query := &plugin.Query{
			Name:     fmt.Sprintf("Query%d", index),
			Cmd:      command,
			Comments: []string{"kind: read"},
		}
		if command == ":one" || command == ":many" || command == ":batchone" || command == ":batchmany" {
			query.Columns = []*plugin.Column{{Name: "id", Type: int8Type, NotNull: true}}
		}
		if command == ":copyfrom" || strings.HasPrefix(command, ":batch") {
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
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(response.GetFiles()[0].GetContents())
	wantSignatures := []string{
		"Query0(ctx context.Context) (int64, error)",
		"Query1(ctx context.Context) ([]int64, error)",
		"Query2(ctx context.Context) error",
		"Query3(ctx context.Context) (int64, error)",
		"Query4(ctx context.Context) (pgconn.CommandTag, error)",
		"Query5(ctx context.Context, id []int64) (int64, error)",
		"Query6(ctx context.Context, id []int64) *Query6BatchResults",
		"Query7(ctx context.Context, id []int64) *Query7BatchResults",
		"Query8(ctx context.Context, id []int64) *Query8BatchResults",
	}
	for index, signature := range wantSignatures {
		if !strings.Contains(got, signature) {
			t.Fatalf("generated output missing command %s signature %q:\n%s", commands[index], signature, got)
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
			"runtime_import_path":"example.test/project/sqlcstore",
			"sql_package":"pgx/v5",
			"query_parameter_limit":1,
			"emit_params_struct_pointers":true,
			"emit_result_struct_pointers":true,
			"overrides":[{"db_type":"public.token","go_type":{"type":"Token"}}]
		}`),
	})
	require.NoError(t, err)
	require.Len(t, response.GetFiles(), 1)
	assert.Equal(t, "generated_store.go", response.GetFiles()[0].GetName())

	got := string(response.GetFiles()[0].GetContents())
	checks := []string{
		`db "example.test/project/internal/db"`,
		`sqlcstore "example.test/project/sqlcstore"`,
		"GetUser(ctx context.Context, arg *db.GetUserParams) (*db.User, error)",
		"User(token db.Token) SK",
		"main *db.Queries",
		"func NewReadQueries(database db.DBTX) *ReadQueries",
		"var _ db.Querier = (*StoreQueries)(nil)",
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

	got := string(response.GetFiles()[0].GetContents())
	checks := []string{
		"FindUser(ctx context.Context, arg *FindUserParams) (*string, error)",
		"ResolveTenant(accountID int64) SK",
		"shardKey := q.resolver.ResolveTenant(arg.AccountID)",
	}
	for _, check := range checks {
		assert.Contains(t, got, check)
	}
}

func TestPostgresTypeCompatibility(t *testing.T) {
	t.Parallel()

	req := &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql"},
		Catalog: &plugin.Catalog{
			DefaultSchema: "public",
			Schemas: []*plugin.Schema{
				{Name: "public", Enums: []*plugin.Enum{{Name: "status"}}, CompositeTypes: []*plugin.CompositeType{{Name: "address"}}},
				{Name: "audit", Enums: []*plugin.Enum{{Name: "event"}}},
			},
		},
	}
	opts := &Options{SQLPackage: "pgx/v5"}
	resolver := &typeResolver{req: req, opts: opts, imports: newImportSet()}
	tests := []struct {
		name    string
		typ     *plugin.Identifier
		notNull bool
		want    string
	}{
		{name: "inet", typ: &plugin.Identifier{Name: "inet"}, notNull: true, want: "netip.Addr"},
		{name: "nullable inet", typ: &plugin.Identifier{Name: "inet"}, want: "*netip.Addr"},
		{name: "cidr", typ: &plugin.Identifier{Name: "cidr"}, notNull: true, want: "netip.Prefix"},
		{name: "mac address", typ: &plugin.Identifier{Name: "macaddr"}, want: "net.HardwareAddr"},
		{name: "timestamp range", typ: &plugin.Identifier{Name: "tstzrange"}, want: "pgtype.Range[pgtype.Timestamptz]"},
		{
			name: "timestamp multirange",
			typ:  &plugin.Identifier{Name: "tstzmultirange"},
			want: "pgtype.Multirange[pgtype.Range[pgtype.Timestamptz]]",
		},
		{name: "bits", typ: &plugin.Identifier{Name: "varbit"}, want: "pgtype.Bits"},
		{name: "xid8", typ: &plugin.Identifier{Name: "xid8"}, want: "pgtype.Uint64"},
		{name: "vector", typ: &plugin.Identifier{Name: "vector"}, want: "pgvector.Vector"},
		{name: "enum", typ: &plugin.Identifier{Schema: "public", Name: "status"}, notNull: true, want: "Status"},
		{name: "nullable enum", typ: &plugin.Identifier{Schema: "public", Name: "status"}, want: "NullStatus"},
		{name: "nondefault schema enum", typ: &plugin.Identifier{Schema: "audit", Name: "event"}, notNull: true, want: "AuditEvent"},
		{name: "nullable composite", typ: &plugin.Identifier{Schema: "public", Name: "address"}, want: "sql.NullString"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, resolver.goType(&plugin.Column{Type: test.typ, NotNull: test.notNull}))
		})
	}
}

func TestColumnOverridePrecedenceAndPatterns(t *testing.T) {
	t.Parallel()

	request := &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql", Codegen: &plugin.Codegen{Out: "db"}},
		Catalog:  &plugin.Catalog{DefaultSchema: "public"},
		PluginOptions: []byte(`{
			"package":"db",
			"sql_package":"pgx/v5",
			"overrides":[
				{"db_type":"pg_catalog.int8","go_type":{"type":"string"}},
				{"column":"*.tenant_?d","go_type":{"type":"uint64"}}
			]
		}`),
	}
	opts, err := parseOptions(request)
	require.NoError(t, err)
	resolver := &typeResolver{req: request, opts: opts, imports: newImportSet()}

	tests := []struct {
		name   string
		column *plugin.Column
		want   string
	}{
		{
			name: "column pattern wins over earlier database type override",
			column: &plugin.Column{
				Name:    "tenant_id",
				Type:    &plugin.Identifier{Schema: "pg_catalog", Name: "int8"},
				NotNull: true,
				Table:   &plugin.Identifier{Schema: "public", Name: "users"},
			},
			want: "uint64",
		},
		{
			name: "column override represents an entire array field",
			column: &plugin.Column{
				Name:      "tenant_id",
				Type:      &plugin.Identifier{Schema: "pg_catalog", Name: "int8"},
				NotNull:   true,
				IsArray:   true,
				ArrayDims: 1,
				Table:     &plugin.Identifier{Schema: "public", Name: "users"},
			},
			want: "uint64",
		},
		{
			name: "database type override remains the fallback",
			column: &plugin.Column{
				Name:    "owner_id",
				Type:    &plugin.Identifier{Schema: "pg_catalog", Name: "int8"},
				NotNull: true,
				Table:   &plugin.Identifier{Schema: "public", Name: "users"},
			},
			want: "string",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, resolver.goType(test.column))
		})
	}
}
