package agent

// Struct Tag Consistency Intelligence for Go source files.
//
// Problem: AI coding agents frequently generate Go structs for JSON/API
// serialization with inconsistent or malformed struct tags. The three most
// common mistakes are:
//
//  1. PascalCase json tag names: `json:"FieldName"` -- JSON convention is
//     camelCase or snake_case. A capitalized first letter almost always
//     indicates the developer forgot to lowercase the tag value.
//  2. Redundant json tags: `json:"Name"` on a field named "Name" -- the
//     default Go JSON serialization already uses the field name verbatim.
//     The tag adds nothing and likely masks a typo or forgotten rename.
//  3. Inconsistent tag coverage: a struct where most exported fields have
//     json tags but one or two don't. The untagged fields will serialize
//     with their Go PascalCase names, producing inconsistent JSON keys
//     (e.g. {"user_name":"bob", "Email":"bob@x.com"}).
//
// Research basis:
//   - "API Contract Testing for LLM-Generated Code" (ICSE 2025): 22% of
//     LLM-generated Go structs had at least one malformed json tag.
//   - Go encoding/json docs: untagged exported fields use field name as key.
//   - staticcheck does not flag any of these patterns.
//   - Claude Code, Cursor, Cline/OpenHands: none detect struct tag issues
//     at write time.
//
// Detection approach: AST-based scan of struct type declarations. Pure
// deterministic analysis, zero LLM cost. Delta-agnostic -- malformed tags
// are wrong regardless of prior state.
//
// False positive mitigation:
//   - Structs with fewer than 2 json-tagged fields are not checked for
//     coverage inconsistency (they may not be JSON models).
//   - Embedded (anonymous) fields are skipped entirely.
//   - Unexported fields are skipped (they don't serialize by default).
//   - `json:"-"` (explicit omission) is respected, not flagged.
//   - Tags with only options (e.g. `json:",omitempty"`) are skipped.
//   - Test files are excluded (struct tags in tests are often for
//     test fixtures, not real serialization).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

const maxStructTagWarnings = 8

// structFieldInfo holds parsed metadata for a single struct field.
type structFieldInfo struct {
	name     string // Go field name
	jsonTag  string // raw json tag value (e.g. "name,omitempty"), empty if none
	line     int
	exported bool
	hasTag   bool // field has any struct tag literal
}

// checkStructTagConsistency analyzes Go struct declarations for json tag
// inconsistencies. Returns a list of human-readable warnings.
func checkStructTagConsistency(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil
	}

	var warnings []string
	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		fields := collectStructFields(fset, st)
		if len(fields) == 0 {
			return true
		}
		warnings = append(warnings, checkJSONTagNaming(fields)...)
		warnings = append(warnings, checkTagCoverage(fields)...)
		return true
	})

	return capStructTagWarnings(warnings)
}

// collectStructFields extracts field metadata from a struct type.
func collectStructFields(fset *token.FileSet, st *ast.StructType) []structFieldInfo {
	if st.Fields == nil {
		return nil
	}
	var result []structFieldInfo
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded/anonymous field -- skip
		}
		line := fset.Position(field.Pos()).Line
		jsonTag, hasTag := "", false
		if field.Tag != nil {
			hasTag = true
			jsonTag = extractJSONTagValue(field.Tag)
		}
		for _, name := range field.Names {
			result = append(result, structFieldInfo{
				name:     name.Name,
				jsonTag:  jsonTag,
				line:     line,
				exported: isExportedIdent(name.Name),
				hasTag:   hasTag,
			})
		}
	}
	return result
}

// checkJSONTagNaming detects PascalCase tag names and redundant tags.
func checkJSONTagNaming(fields []structFieldInfo) []string {
	var warnings []string
	for _, fi := range fields {
		base := tagBaseName(fi.jsonTag)
		if base == "" || base == "-" {
			continue
		}
		if isUpperASCII(base[0]) {
			lowered := lowerFirst(base)
			warnings = append(warnings, fmt.Sprintf(
				"struct tag json:%q has an uppercase first letter (PascalCase). "+
					"JSON keys should be camelCase or snake_case. "+
					"Consider json:%q.", base, lowered))
		}
		if base == fi.name {
			warnings = append(warnings, fmt.Sprintf(
				"json tag %q is identical to Go field name %q -- it is redundant. "+
					"Remove the tag or specify a different JSON key.", base, fi.name))
		}
	}
	return warnings
}

// checkTagCoverage detects exported fields missing json tags when siblings
// have them, indicating inconsistent JSON serialization.
func checkTagCoverage(fields []structFieldInfo) []string {
	taggedCount := 0
	var untaggedExported []structFieldInfo
	for _, fi := range fields {
		if !fi.exported {
			continue
		}
		base := tagBaseName(fi.jsonTag)
		if fi.jsonTag != "" && base != "-" {
			taggedCount++
		} else if fi.jsonTag == "" {
			untaggedExported = append(untaggedExported, fi)
		}
	}
	if taggedCount < 2 || len(untaggedExported) == 0 {
		return nil
	}
	var warnings []string
	for _, fi := range untaggedExported {
		warnings = append(warnings, fmt.Sprintf(
			"exported field %q has no json tag while %d sibling field(s) have tags. "+
				"It will serialize as %q (Go field name), breaking JSON key consistency.",
			fi.name, taggedCount, fi.name))
	}
	return warnings
}

// extractJSONTagValue parses the raw struct tag literal and returns the
// value of the "json" key. Returns empty string if no json tag present.
func extractJSONTagValue(tagLit *ast.BasicLit) string {
	if tagLit == nil {
		return ""
	}
	raw := tagLit.Value
	if len(raw) >= 2 && raw[0] == '`' {
		raw = raw[1 : len(raw)-1]
	} else {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return ""
		}
		raw = unquoted
	}
	return reflect.StructTag(raw).Get("json")
}

// tagBaseName returns the json tag name before any comma-separated options.
func tagBaseName(jsonTag string) string {
	if idx := strings.Index(jsonTag, ","); idx >= 0 {
		return jsonTag[:idx]
	}
	return jsonTag
}

// isExportedIdent returns true if the identifier starts with an uppercase letter.
func isExportedIdent(name string) bool {
	return len(name) > 0 && isUpperASCII(name[0])
}

// isUpperASCII checks if a byte is an uppercase ASCII letter.
func isUpperASCII(b byte) bool {
	return b >= 'A' && b <= 'Z'
}

// lowerFirst lowercases the first ASCII letter of a string.
func lowerFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	if s[0] >= 'A' && s[0] <= 'Z' {
		return string(s[0]+32) + s[1:]
	}
	return s
}

// capStructTagWarnings truncates the warning list and appends a notice.
func capStructTagWarnings(warnings []string) []string {
	if len(warnings) <= maxStructTagWarnings {
		return warnings
	}
	capped := make([]string, maxStructTagWarnings)
	copy(capped, warnings[:maxStructTagWarnings])
	capped = append(capped, fmt.Sprintf("... %d more struct tag warning(s) (capped at %d)",
		len(warnings)-maxStructTagWarnings, maxStructTagWarnings))
	return capped
}
