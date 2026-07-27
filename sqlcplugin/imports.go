package sqlcplugin

import (
	"sort"
	"strings"
)

type importSpec struct {
	name string
	path string
}

type importSet struct {
	byPath map[string]importSpec
}

func newImportSet() *importSet {
	return &importSet{byPath: map[string]importSpec{}}
}

func (s *importSet) add(path string) {
	s.addNamed("", path)
}

func (s *importSet) addNamed(name, path string) {
	if path == "" {
		return
	}
	s.byPath[path] = importSpec{name: name, path: path}
}

func (s *importSet) addForType(typ string) {
	for qualifier, path := range knownSelectorImports {
		if typeUsesQualifier(typ, qualifier) {
			s.add(path)
		}
	}
}

func (s *importSet) sorted() []importSpec {
	imports := make([]importSpec, 0, len(s.byPath))
	for _, imp := range s.byPath {
		imports = append(imports, imp)
	}
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].path == imports[j].path {
			return imports[i].name < imports[j].name
		}
		return imports[i].path < imports[j].path
	})
	return imports
}

var knownSelectorImports = map[string]string{
	"driver":            "database/sql/driver",
	defaultContext:      defaultContext,
	"json":              "encoding/json",
	"net":               "net",
	"netip":             "net/netip",
	defaultSQLQualifier: defaultDatabaseSQL,
	"time":              "time",
	"uuid":              "github.com/google/uuid",
	"pgconn":            "github.com/jackc/pgx/v5/pgconn",
	"pgtype":            "github.com/jackc/pgx/v5/pgtype",
	"pgvector":          "github.com/pgvector/pgvector-go",
	"pqtype":            "github.com/sqlc-dev/pqtype",
}

func typeUsesQualifier(typ, qualifier string) bool {
	return strings.Contains(typ, qualifier+".") || strings.Contains(typ, "*"+qualifier+".") ||
		strings.Contains(typ, "[]"+qualifier+".")
}
