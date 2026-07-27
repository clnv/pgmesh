package sqlcplugin

import (
	"testing"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresTypeCompatibility(t *testing.T) {
	t.Parallel()

	request := &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: "postgresql"},
		Catalog: &plugin.Catalog{
			DefaultSchema: "public",
			Schemas: []*plugin.Schema{
				{
					Name:           "public",
					Enums:          []*plugin.Enum{{Name: "status"}},
					CompositeTypes: []*plugin.CompositeType{{Name: "address"}},
				},
				{Name: "audit", Enums: []*plugin.Enum{{Name: "event"}}},
			},
		},
	}
	tests := []struct {
		name             string
		typ              *plugin.Identifier
		notNull          bool
		arrayDimensions  int32
		emitPointers     bool
		emitEnumPointers *bool
		want             string
	}{
		{name: "bigint", typ: &plugin.Identifier{Name: "int8"}, notNull: true, want: "int64"},
		{name: "nullable bigint", typ: &plugin.Identifier{Name: "int8"}, want: "pgtype.Int8"},
		{name: "nullable bigint pointer", typ: &plugin.Identifier{Name: "int8"}, emitPointers: true, want: "*int64"},
		{name: "bigint array", typ: &plugin.Identifier{Name: "int8"}, arrayDimensions: 1, want: "[]int64"},
		{name: "nullable text", typ: &plugin.Identifier{Name: "text"}, want: "pgtype.Text"},
		{name: "json", typ: &plugin.Identifier{Name: "jsonb"}, want: "[]byte"},
		{name: "date", typ: &plugin.Identifier{Name: "date"}, want: "pgtype.Date"},
		{name: "time", typ: &plugin.Identifier{Schema: "pg_catalog", Name: "time"}, want: "pgtype.Time"},
		{name: "timestamp", typ: &plugin.Identifier{Name: "timestamp"}, want: "pgtype.Timestamp"},
		{name: "timestamp with timezone", typ: &plugin.Identifier{Name: "timestamptz"}, want: "pgtype.Timestamptz"},
		{name: "uuid", typ: &plugin.Identifier{Name: "uuid"}, want: "pgtype.UUID"},
		{name: "numeric", typ: &plugin.Identifier{Name: "numeric"}, want: "pgtype.Numeric"},
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
		{
			name:             "nullable enum pointer",
			typ:              &plugin.Identifier{Schema: "public", Name: "status"},
			emitEnumPointers: new(true),
			want:             "*Status",
		},
		{
			name:             "nullable enum override disables pointer",
			typ:              &plugin.Identifier{Schema: "public", Name: "status"},
			emitPointers:     true,
			emitEnumPointers: new(false),
			want:             "NullStatus",
		},
		{
			name:    "nondefault schema enum",
			typ:     &plugin.Identifier{Schema: "audit", Name: "event"},
			notNull: true,
			want:    "AuditEvent",
		},
		{
			name: "nullable composite",
			typ:  &plugin.Identifier{Schema: "public", Name: "address"},
			want: "sql.NullString",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver := &typeResolver{
				req: request,
				opts: &options{
					SQLPackage:                  defaultSQLPackage,
					EmitPointersForNullTypes:    test.emitPointers,
					EmitPointersForNullEnumType: test.emitEnumPointers,
				},
				imports: newImportSet(),
			}
			column := &plugin.Column{
				Type:      test.typ,
				NotNull:   test.notNull,
				IsArray:   test.arrayDimensions > 0,
				ArrayDims: test.arrayDimensions,
			}

			assert.Equal(t, test.want, resolver.goType(column))
		})
	}
}

func TestGoTypeEncodings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		goTypeJSON string
		wantType   string
		wantImport string
		wantAlias  string
	}{
		{name: "basic string", goTypeJSON: `"string"`, wantType: "string"},
		{name: "standard package string", goTypeJSON: `"time.Time"`, wantType: "time.Time", wantImport: "time"},
		{
			name:       "module package string",
			goTypeJSON: `"github.com/google/uuid.UUID"`,
			wantType:   "uuid.UUID",
			wantImport: "github.com/google/uuid",
		},
		{
			name:       "pointer module string",
			goTypeJSON: `"*github.com/google/uuid.UUID"`,
			wantType:   "*uuid.UUID",
			wantImport: "github.com/google/uuid",
		},
		{name: "local map", goTypeJSON: `{"type":"LocalID"}`, wantType: "LocalID"},
		{
			name:       "aliased map",
			goTypeJSON: `{"import":"example.test/types","package":"domain","type":"ID","pointer":true,"slice":true}`,
			wantType:   "[]*domain.ID",
			wantImport: "example.test/types",
			wantAlias:  "domain",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := optionsRequest(`{"overrides":[{"db_type":"text","go_type":` + test.goTypeJSON + `}]}`)

			opts, err := parseOptions(request)

			require.NoError(t, err)
			require.Len(t, opts.Overrides, 1)
			assert.Equal(t, test.wantType, opts.Overrides[0].typeName)
			assert.Equal(t, test.wantImport, opts.Overrides[0].importPath)
			assert.Equal(t, test.wantAlias, opts.Overrides[0].importAlias)
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
