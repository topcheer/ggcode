package agent

// Unkeyed Struct Initialization Detection (Check #67)
//
// Problem: AI coding agents frequently generate Go composite literals that
// initialize structs positionally (unkeyed) rather than by field name:
//
//	type Config struct {
//	    Host string
//	    Port int
//	    TLS  bool
//	}
//
//	// Bad -- unkeyed: fragile, error-prone
//	c := Config{"localhost", 8080, false}
//
//	// Good -- keyed
//	c := Config{Host: "localhost", Port: 8080, TLS: false}
//
// Unkeyed initialization is dangerous because:
//  1. If struct fields are reordered, added, or removed, the code silently
//     compiles but produces wrong values -- one of the hardest bugs to catch.
//  2. It's unreadable: the reader cannot tell which value maps to which field.
//  3. It violates Go idioms -- the Go style guide explicitly recommends keyed
//     struct initialization.
//
// Competitor analysis:
//   - go vet -composites: only checks whitelisted stdlib packages (e.g.,
//     flag.FlagSet, time.Time). Does NOT check user-defined types.
//   - staticcheck: does not check this (SA1xxx focuses on other patterns)
//   - golangci-lint: delegates to go vet composites (same limitation)
//   - gocritic: no unkeyed composite check
//   - Claude Code/Cursor/Aider/OpenHands: no write-time detection
//
// Approach: AST-based analysis, zero LLM cost. Walks the parsed AST for
// *ast.CompositeLit nodes, checks if the type resolves to a struct type,
// and flags when elements are positional (no *ast.KeyValueExpr). Delta-aware:
// only flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	unkeyedMaxWarnings  = 4
	unkeyedMinFieldSkip = 1 // skip structs with fewer fields (e.g., wrapper{})
)

// unkeyedIssue records a single unkeyed struct initialization.
type unkeyedIssue struct {
	line       int
	typeName   string
	fieldCount int
}

// checkUnkeyedStruct detects unkeyed struct initialization in Go code.
// Returns warning strings. Delta-aware: only flags NEW instances.
func checkUnkeyedStruct(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	newIssues := findUnkeyedStructs(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: subtract issues present in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldIssues := findUnkeyedStructs(filePath, oldContent)
		if len(oldIssues) > 0 {
			oldSet := make(map[string]bool, len(oldIssues))
			for _, oi := range oldIssues {
				oldSet[unkeyedIssueKey(oi)] = true
			}
			filtered := newIssues[:0]
			for _, ni := range newIssues {
				if !oldSet[unkeyedIssueKey(ni)] {
					filtered = append(filtered, ni)
				}
			}
			newIssues = filtered
		}
	}

	if len(newIssues) == 0 {
		return nil
	}
	return buildUnkeyedWarnings(newIssues)
}

// unkeyedIssueKey creates a deduplication key for delta comparison.
// Uses type name only (not line number) so line shifts from unrelated edits
// don't break delta filtering.
func unkeyedIssueKey(i unkeyedIssue) string {
	return fmt.Sprintf("unkeyed:%s:%d", i.typeName, i.fieldCount)
}

// buildUnkeyedWarnings converts issues into human-readable warning strings.
func buildUnkeyedWarnings(issues []unkeyedIssue) []string {
	var warnings []string
	for i, issue := range issues {
		if i >= unkeyedMaxWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"...and %d more unkeyed struct initialization(s) omitted",
				len(issues)-unkeyedMaxWarnings))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"Unkeyed struct initialization at L%d: %s{%d positional values}. "+
				"Use field names: %s{Field1: val1, ...}. "+
				"Positional init breaks silently if fields are reordered or added.",
			issue.line, issue.typeName, issue.fieldCount, issue.typeName))
	}
	return warnings
}

// findUnkeyedStructs parses Go source and finds all unkeyed struct
// initialization patterns.
func findUnkeyedStructs(filename, src string) []unkeyedIssue {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Build a set of known struct type names declared in this file.
	structTypes := collectStructTypes(file)

	var issues []unkeyedIssue
	ast.Inspect(file, func(n ast.Node) bool {
		comp, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		issue, found := analyzeComposite(comp, fset, structTypes)
		if found {
			issues = append(issues, issue)
		}
		return true
	})

	return issues
}

// collectStructTypes builds a map of struct type names to their field count.
func collectStructTypes(file *ast.File) map[string]int {
	result := make(map[string]int)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			result[ts.Name.Name] = structFieldCount(st)
		}
	}
	return result
}

// structFieldCount counts the actual number of named fields in a struct,
// handling the case where multiple fields share one type (e.g., `A, B, C int`).
func structFieldCount(st *ast.StructType) int {
	if st.Fields == nil {
		return 0
	}
	count := 0
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			// Embedded field (e.g., `http.Client`) counts as 1.
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

// analyzeComposite checks a single composite literal for unkeyed struct init.
func analyzeComposite(comp *ast.CompositeLit, fset *token.FileSet, structTypes map[string]int) (unkeyedIssue, bool) {
	if len(comp.Elts) == 0 {
		return unkeyedIssue{}, false
	}

	typeName := resolveCompositeTypeName(comp)
	if typeName == "" {
		return unkeyedIssue{}, false
	}

	// Only flag types we know are structs (declared in this file).
	_, isStruct := structTypes[typeName]
	if !isStruct {
		// Could be a struct from another package (e.g., pkg.Type{...}).
		// We can't confirm without full type checking, so we check the
		// heuristic: if ALL elements are non-keyvalue expressions, it's
		// likely positional struct init.
		if !isQualifiedTypeName(typeName) {
			return unkeyedIssue{}, false
		}
	}

	// Check if all elements are positional (no KeyValueExpr).
	if !allElementsUnkeyed(comp.Elts) {
		return unkeyedIssue{}, false
	}

	// Skip very small structs (1 field) -- unkeyed is trivially correct.
	if isStruct {
		fieldCount := structTypes[typeName]
		if fieldCount <= unkeyedMinFieldSkip {
			return unkeyedIssue{}, false
		}
	}

	pos := fset.Position(comp.Pos())
	return unkeyedIssue{
		line:       pos.Line,
		typeName:   typeName,
		fieldCount: len(comp.Elts),
	}, true
}

// resolveCompositeTypeName extracts the type name from a composite literal's
// Type field. Handles *ast.Ident and *ast.SelectorExpr.
func resolveCompositeTypeName(comp *ast.CompositeLit) string {
	switch t := comp.Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name + "." + t.Sel.Name
		}
	}
	return ""
}

// isQualifiedTypeName returns true if the type name contains a package
// qualifier (e.g., "http.Client").
func isQualifiedTypeName(name string) bool {
	return strings.Contains(name, ".")
}

// allElementsUnkeyed returns true if ALL elements in the composite literal
// are positional (not *ast.KeyValueExpr).
func allElementsUnkeyed(elts []ast.Expr) bool {
	for _, el := range elts {
		if _, ok := el.(*ast.KeyValueExpr); ok {
			return false
		}
	}
	return true
}
