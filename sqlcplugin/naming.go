package sqlcplugin

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
)

func columnName(column *plugin.Column, pos int) string {
	if column.GetName() != "" {
		return column.GetName()
	}
	return fmt.Sprintf("column_%d", pos+1)
}

func paramName(param *plugin.Parameter) string {
	if param.GetColumn().GetName() != "" {
		return argName(param.GetColumn().GetName())
	}
	return fmt.Sprintf("dollar_%d", param.GetNumber())
}

func argName(name string) string {
	var out strings.Builder
	for idx, part := range strings.Split(name, "_") {
		switch {
		case idx == 0:
			out.WriteString(strings.ToLower(part))
		case strings.EqualFold(part, "id"):
			out.WriteString("ID")
		default:
			out.WriteString(title(part))
		}
	}
	return out.String()
}

func structName(name string, opts *options) string {
	if replacement := opts.Rename[name]; replacement != "" {
		return replacement
	}
	parts := splitIdentifier(name)
	for idx, part := range parts {
		lower := strings.ToLower(part)
		if strings.EqualFold(lower, "id") {
			parts[idx] = "ID"
			continue
		}
		parts[idx] = title(lower)
	}
	result := strings.Join(parts, "")
	first, _ := utf8.DecodeRuneInString(result)
	if unicode.IsDigit(first) {
		return "_" + result
	}
	return result
}

func splitIdentifier(name string) []string {
	var parts []string
	var current strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
			continue
		}
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func title(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func lowerTitle(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func snakeCaseIdentifier(name string) string {
	runes := []rune(name)
	var out strings.Builder
	for index, current := range runes {
		if index > 0 && unicode.IsUpper(current) {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
				unicode.IsUpper(previous) && nextIsLower {
				out.WriteByte('_')
			}
		}
		out.WriteRune(unicode.ToLower(current))
	}
	return out.String()
}

func escape(name string) string {
	if goKeywords[name] {
		return name + "_"
	}
	return name
}

var goKeywords = map[string]bool{
	"break":       true,
	"default":     true,
	"func":        true,
	"interface":   true,
	"select":      true,
	"case":        true,
	"defer":       true,
	"go":          true,
	"map":         true,
	"struct":      true,
	"chan":        true,
	"else":        true,
	"goto":        true,
	"package":     true,
	"switch":      true,
	"const":       true,
	"fallthrough": true,
	"if":          true,
	"range":       true,
	"type":        true,
	"continue":    true,
	"for":         true,
	"import":      true,
	"return":      true,
	"var":         true,
}

func packageNameForImport(path string) string {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "go-")
	base = strings.TrimSuffix(base, "-go")
	if strings.HasPrefix(base, "v") && len(base) > 1 && isDigits(base[1:]) {
		parent := filepath.Base(filepath.Dir(path))
		parent = strings.TrimPrefix(parent, "go-")
		parent = strings.TrimSuffix(parent, "-go")
		return strings.Map(importPackageRune, parent)
	}
	return strings.Map(importPackageRune, base)
}

func importPackageRune(r rune) rune {
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return unicode.ToLower(r)
	}
	return '_'
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}
