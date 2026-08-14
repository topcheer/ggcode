package agent

// Sensitive Field Exposure in JSON (OWASP A01:2021 -- Broken Access Control)
//
// Problem: AI coding agents frequently generate Go structs with sensitive
// fields (Password, Token, ApiKey, Secret, Credentials) and add json tags
// like `json:"password"` or `json:"apiKey"` -- without `json:"-"` to exclude
// them from JSON serialization. This causes sensitive data to leak into:
//   - API responses (HTTP JSON bodies)
//   - Log output (structured logging that marshals structs)
//   - Error messages that embed struct values
//
// Real-world impact: this is one of the most common causes of data breaches.
// OWASP A01:2021 lists Broken Access Control as the #1 web app security risk.
//
// Competitor analysis:
//   - Claude Code: no inline detection
//   - Cursor: relies on external lint-on-save rules (gosec G104 -- different)
//   - Cline/OpenHands: reactive only
//   - Aider: no detection
//   - Windsurf: no detection
//   - gosec: does not check struct tag sensitivity
//
// None provide write-time inline detection. This check uses Go AST to:
//  1. Identify struct fields with names matching sensitive patterns
//  2. Check if they have a json tag that doesn't use "-" (skip)
//  3. Report them so the agent can fix by adding json:"-"
//
// Zero LLM cost. Runs in <1ms per file.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// sensitiveJSONMaxWarnings caps the number of warnings.
const sensitiveJSONMaxWarnings = 4

// sensitiveFieldNames lists substrings that indicate a field holds
// sensitive data. Matched case-insensitively against the Go field name
// and the json tag name.
var sensitiveFieldPatterns = []string{
	"password", "passwd", "secret", "token", "apikey", "api_key",
	"accesstoken", "access_token", "refreshtoken", "refresh_token",
	"privatekey", "private_key", "credential", "ssn", "creditcard",
}

// isSensitiveFieldName checks if a field name contains a sensitive pattern.
func isSensitiveFieldName(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range sensitiveFieldPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// extractJSONTagName parses the json struct tag and returns the field name
// portion (before the first comma). Returns the raw tag value and whether
// a json tag exists.
func extractJSONTagName(tag string) (name string, hasTag bool) {
	// Go struct tags look like: `json:"fieldName,omitempty"`
	// We need to extract the json portion.
	if !strings.Contains(tag, `json:"`) {
		return "", false
	}
	idx := strings.Index(tag, `json:"`)
	if idx < 0 {
		return "", false
	}
	rest := tag[idx+6:] // skip `json:"`
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", false
	}
	val := rest[:end]
	// The tag value may be "fieldName,omitempty" -- take the part before comma
	if comma := strings.Index(val, ","); comma >= 0 {
		val = val[:comma]
	}
	return val, true
}

// sensitiveFieldWarning builds a warning string for a sensitive field exposure.
// Returns "" if the field is not sensitive or already properly excluded.
func sensitiveFieldWarning(fset *token.FileSet, filePath, structName string, field *ast.Field) string {
	if field.Tag == nil {
		return ""
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
		"%s:%d: sensitive field %q in struct %q has json tag %q but is not excluded from JSON output -- add `json:\"-\"` to prevent sensitive data exposure in API responses (OWASP A01:2021)",
		filepath.Base(filePath), pos.Line, displayName, structName, tagName,
	)
}

// sensitiveJSONOldKeys parses oldContent and returns the set of
// struct.field keys that already carried sensitive warnings (fix #173).
func sensitiveJSONOldKeys(filePath, oldContent string) map[string]bool {
	keys := make(map[string]bool)
	if strings.TrimSpace(oldContent) == "" {
		return keys
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, oldContent, 0)
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

// that are not excluded from JSON serialization (missing json:"-").
func checkSensitiveJSONExposure(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, parser.ParseComments)
	if err != nil {
		return nil
	}

	// Delta-aware (fix #173): suppress warnings for struct.field pairs that
	// already existed in the old content — key on names, not positions, so
	// line shifts don't re-flag legacy fields on every edit.
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
			// Embedded (anonymous) fields have empty Names — index access would
			// panic and the check-registry recover would then swallow ALL
			// sensitive-field warnings for the write (fix #173).
			fieldName := ""
			if len(field.Names) > 0 {
				fieldName = field.Names[0].Name
			} else if id, ok := field.Type.(*ast.Ident); ok {
				fieldName = id.Name // embedded type name as fallback key
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
