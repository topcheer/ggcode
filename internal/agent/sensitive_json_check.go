package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const sensitiveJSONMaxWarnings = 4

var sensitiveFieldPatterns = []string{
	"password", "passwd", "secret", "token", "apikey", "api_key",
	"accesstoken", "access_token", "refreshtoken", "refresh_token",
	"privatekey", "private_key", "credential", "ssn", "creditcard",
}

var tokenCounterSuffixes = []string{
	"tokens", "tokencount", "tokenusage", "tokenlimit", "tokenbudget",
	"tokenleft", "tokenremaining", "tokenprice", "tokencost",
	"tokensleft", "tokensremaining",
}

func isSensitiveFieldName(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range sensitiveFieldPatterns {
		if pat == "token" && hasTokenCounterSuffix(lower) {
			continue
		}
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func hasTokenCounterSuffix(lower string) bool {
	for _, suf := range tokenCounterSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

func extractJSONTagName(tag string) (name string, hasTag bool) {

	if !strings.Contains(tag, `json:"`) {
		return "", false
	}
	idx := strings.Index(tag, `json:"`)
	if idx < 0 {
		return "", false
	}
	rest := tag[idx+6:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", false
	}
	val := rest[:end]

	if comma := strings.Index(val, ","); comma >= 0 {
		val = val[:comma]
	}
	return val, true
}

func sensitiveFieldWarning(fset *token.FileSet, filePath, structName string, field *ast.Field) string {
	if field.Tag == nil {

		fieldName := ""
		if len(field.Names) > 0 {
			fieldName = field.Names[0].Name
		}
		if fieldName == "" || !isSensitiveFieldName(fieldName) || !field.Names[0].IsExported() {
			return ""
		}
		pos := fset.Position(field.Pos())
		return fmt.Sprintf(
			"%s:%d: sensitive field %q in struct %q has NO json tag - encoding/json serializes it under the field name, leaking it in any marshaled output. If this struct crosses a response/log boundary, add `json:\"-\"` (or omit the field); if it MUST serialize (request payloads, token exchanges, persisted config), verify every sink redacts it before relying on it (OWASP A01:2021)",
			filepath.Base(filePath), pos.Line, fieldName, structName,
		)
	}
	tagName, hasJSONTag := extractJSONTagName(field.Tag.Value)
	if !hasJSONTag || tagName == "-" {
		return ""
	}

	fieldName := ""
	if len(field.Names) > 0 {
		fieldName = field.Names[0].Name
	}

	sensitive := (fieldName != "" && isSensitiveFieldName(fieldName)) ||
		(tagName != "" && isSensitiveFieldName(tagName))
	if !sensitive {
		return ""
	}

	displayName := tagName
	if displayName == "" {
		displayName = fieldName
	}
	pos := fset.Position(field.Pos())
	return fmt.Sprintf(
		"%s:%d: sensitive field %q in struct %q has json tag %q and IS serialized - if this struct crosses a response/log boundary add `json:\"-\"` to exclude it; if it must serialize (request payload, token exchange, persisted config), verify every consumer/redaction sink handles it before shipping (OWASP A01:2021)",
		filepath.Base(filePath), pos.Line, displayName, structName, tagName,
	)
}

func sensitiveJSONOldKeys(filePath, oldContent string) map[string]bool {
	keys := make(map[string]bool)
	if strings.TrimSpace(oldContent) == "" {
		return keys
	}
	file, fset, err := parseGoSource(filePath, oldContent, 0)
	if err != nil {
		return keys
	}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, field := range st.Fields.List {
			if sensitiveFieldWarning(fset, filePath, ts.Name.Name, field) == "" {
				continue
			}
			if len(field.Names) > 0 {
				keys[ts.Name.Name+"."+field.Names[0].Name] = true
			} else if id, ok := field.Type.(*ast.Ident); ok {
				keys[ts.Name.Name+"."+id.Name] = true
			}
		}
		return true
	})
	return keys
}

func checkSensitiveJSONExposure(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	file, fset, err := parseGoSource(filePath, newContent, parser.ParseComments)
	if err != nil {
		return nil
	}

	oldSeen := sensitiveJSONOldKeys(filePath, oldContent)

	var warnings []string
	seen := make(map[string]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}

		structName := ts.Name.Name
		for _, field := range st.Fields.List {
			w := sensitiveFieldWarning(fset, filePath, structName, field)
			if w == "" {
				continue
			}

			fieldName := ""
			if len(field.Names) > 0 {
				fieldName = field.Names[0].Name
			} else if id, ok := field.Type.(*ast.Ident); ok {
				fieldName = id.Name
			}
			key := structName + "." + fieldName
			if seen[key] || oldSeen[key] {
				continue
			}
			seen[key] = true
			warnings = append(warnings, w)
			if len(warnings) >= sensitiveJSONMaxWarnings {
				return false
			}
		}
		return true
	})

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}
