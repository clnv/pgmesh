package sqlcplugin

import (
	"strings"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
)

type sqlRelationToken struct {
	value      string
	identifier bool
	quoted     bool
}

func queryRelationIdentifiers(sql string) []*plugin.Identifier {
	tokens := tokenizeSQLRelations(sql)
	relations := make([]*plugin.Identifier, 0)
	for index, token := range tokens {
		if !token.identifier || token.quoted {
			continue
		}
		switch token.value {
		case "from", "into", "join", "update":
		default:
			continue
		}

		next := index + 1
		for next < len(tokens) &&
			tokens[next].identifier &&
			!tokens[next].quoted &&
			(tokens[next].value == "lateral" || tokens[next].value == "only") {
			next++
		}
		if next >= len(tokens) || !tokens[next].identifier {
			continue
		}

		parts := []string{tokens[next].value}
		for len(parts) < 3 &&
			next+2 < len(tokens) &&
			tokens[next+1].value == "." &&
			tokens[next+2].identifier {
			parts = append(parts, tokens[next+2].value)
			next += 2
		}
		switch len(parts) {
		case 1:
			relations = append(relations, &plugin.Identifier{
				Catalog: "",
				Schema:  "",
				Name:    parts[0],
			})
		case 2:
			relations = append(relations, &plugin.Identifier{
				Catalog: "",
				Schema:  parts[0],
				Name:    parts[1],
			})
		case 3:
			relations = append(relations, &plugin.Identifier{
				Catalog: parts[0],
				Schema:  parts[1],
				Name:    parts[2],
			})
		}
	}
	return relations
}

func tokenizeSQLRelations(sql string) []sqlRelationToken {
	tokens := make([]sqlRelationToken, 0)
	for index := 0; index < len(sql); {
		switch {
		case isSQLSpace(sql[index]):
			index++
		case strings.HasPrefix(sql[index:], "--"):
			index = skipSQLLineComment(sql, index+2)
		case strings.HasPrefix(sql[index:], "/*"):
			index = skipSQLBlockComment(sql, index+2)
		case sql[index] == '\'':
			index = skipSQLSingleQuotedString(sql, index+1)
		case sql[index] == '"':
			var value string
			value, index = readSQLQuotedIdentifier(sql, index+1)
			tokens = append(tokens, sqlRelationToken{
				value:      value,
				identifier: true,
				quoted:     true,
			})
		case sql[index] == '$':
			if next, ok := skipSQLDollarQuotedString(sql, index); ok {
				index = next
			} else {
				index++
			}
		case isSQLIdentifierStart(sql[index]):
			start := index
			index++
			for index < len(sql) && isSQLIdentifierPart(sql[index]) {
				index++
			}
			tokens = append(tokens, sqlRelationToken{
				value:      strings.ToLower(sql[start:index]),
				identifier: true,
				quoted:     false,
			})
		default:
			if sql[index] == '.' || sql[index] == '(' {
				tokens = append(tokens, sqlRelationToken{
					value:      string(sql[index]),
					identifier: false,
					quoted:     false,
				})
			}
			index++
		}
	}
	return tokens
}

func skipSQLLineComment(sql string, index int) int {
	for index < len(sql) && sql[index] != '\n' {
		index++
	}
	return index
}

func skipSQLBlockComment(sql string, index int) int {
	depth := 1
	for index < len(sql) && depth > 0 {
		switch {
		case strings.HasPrefix(sql[index:], "/*"):
			depth++
			index += 2
		case strings.HasPrefix(sql[index:], "*/"):
			depth--
			index += 2
		default:
			index++
		}
	}
	return index
}

func skipSQLSingleQuotedString(sql string, index int) int {
	for index < len(sql) {
		if sql[index] != '\'' {
			index++
			continue
		}
		if index+1 < len(sql) && sql[index+1] == '\'' {
			index += 2
			continue
		}
		return index + 1
	}
	return index
}

func readSQLQuotedIdentifier(sql string, index int) (string, int) {
	var value strings.Builder
	for index < len(sql) {
		if sql[index] != '"' {
			value.WriteByte(sql[index])
			index++
			continue
		}
		if index+1 < len(sql) && sql[index+1] == '"' {
			value.WriteByte('"')
			index += 2
			continue
		}
		return value.String(), index + 1
	}
	return value.String(), index
}

func skipSQLDollarQuotedString(sql string, index int) (int, bool) {
	delimiterEnd := index + 1
	for delimiterEnd < len(sql) && isSQLDollarTagPart(sql[delimiterEnd]) {
		delimiterEnd++
	}
	if delimiterEnd >= len(sql) || sql[delimiterEnd] != '$' {
		return index, false
	}
	delimiter := sql[index : delimiterEnd+1]
	if closing := strings.Index(sql[delimiterEnd+1:], delimiter); closing >= 0 {
		return delimiterEnd + 1 + closing + len(delimiter), true
	}
	return len(sql), true
}

func isSQLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func isSQLIdentifierStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func isSQLIdentifierPart(value byte) bool {
	return isSQLIdentifierStart(value) ||
		value >= '0' && value <= '9' ||
		value == '$'
}

func isSQLDollarTagPart(value byte) bool {
	return isSQLIdentifierStart(value) || value >= '0' && value <= '9'
}
