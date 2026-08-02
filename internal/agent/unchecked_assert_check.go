package agent

// Unchecked Type Assertion Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code with unchecked type
// assertions: `val := x.(SomeType)` without the comma-ok idiom. If the
// assertion fails at runtime, Go panics with "interface conversion" - a crash
// that is often not caught by tests (the failing input may be rare in the
// happy path) and manifests as a production incident.
//
// The safe pattern is the comma-ok form: `val, ok := x.(SomeType)` which
// returns a zero value and `false` instead of panicking, allowing graceful
// handling.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via go vet)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//
// External linters (errcheck, staticcheck S1033) can catch some cases but
// require a separate lint cycle and are not always installed. go vet does
// not flag type assertions at all. This check provides immediate, zero-
// dependency feedback at write time using Go stdlib AST parser.
//
// Approach: AST-based analysis. Walk all assignments and value specs looking
// for TypeAssertExpr in single-value context (not comma-ok). The comma-ok form
// produces an AssignStmt with two LHS operands; the unchecked form has one.
// We also catch unchecked assertions in function call arguments and return
// statements where the panic would propagate to the caller. Delta-aware: only
// flags NEW unchecked assertions introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// uncheckedAssertInfo records a single unchecked type assertion with its line.
type uncheckedAssertInfo struct {
	line int
}

// checkUncheckedTypeAssert detects unchecked type assertions in Go code.
// Returns warning strings. Only flags NEW instances introduced by this edit
// (delta-aware).
func checkUncheckedTypeAssert(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldAsserts := findUncheckedAsserts(filePath, oldContent)
	newAsserts := findUncheckedAsserts(filePath, newContent)

	// Delta: only flag if NEW has more unchecked assertions than OLD.
	if len(newAsserts) <= len(oldAsserts) {
		return nil
	}

	introduced := len(newAsserts) - len(oldAsserts)
	noun := "unchecked type assertion"
	if introduced > 1 {
		noun = "unchecked type assertions"
	}

	return []string{
		fmt.Sprintf(
			"Introduced %d %s: x.(T) without comma-ok guard. "+
				"If the assertion fails, Go panics with 'interface conversion'. "+
				"Use the comma-ok form: val, ok := x.(T) and handle !ok gracefully.",
			introduced, noun),
	}
}

// findUncheckedAsserts parses Go source and returns all unchecked type
// assertions. An assertion is "unchecked" when it appears in a single-value
// context (one LHS in an assignment, or in a call argument / return / send).
// The comma-ok form (`val, ok := x.(T)`) has two LHS operands and is safe.
func findUncheckedAsserts(filename, src string) []uncheckedAssertInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Collect positions of all comma-ok type assertions so we can exclude them.
	okAssertions := make(map[token.Pos]bool)

	// Collect positions of all type assertions found anywhere.
	allAssertions := make(map[token.Pos]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		ta, ok := n.(*ast.TypeAssertExpr)
		if !ok {
			return true
		}
		// Skip type switch guards: `switch t := v.(type)` produces a
		// TypeAssertExpr with nil Type. These are always safe.
		if ta.Type == nil {
			return true
		}
		allAssertions[ta.Pos()] = true
		return true
	})

	// Mark comma-ok assertions: assignments with 2 LHS where one RHS is a
	// TypeAssertExpr.
	for _, decl := range file.Decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			// Comma-ok: exactly 2 LHS and 1 RHS that is a TypeAssertExpr.
			if len(assign.Lhs) == 2 && len(assign.Rhs) == 1 {
				if ta, ok := assign.Rhs[0].(*ast.TypeAssertExpr); ok {
					okAssertions[ta.Pos()] = true
				}
			}
			return true
		})
	}

	// Also check ValueSpec (var declarations) for comma-ok form.
	// Rare but valid Go syntax.
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// Comma-ok in var decl: 2 names, 1 value that is TypeAssertExpr.
			if len(vs.Names) == 2 && len(vs.Values) == 1 {
				if ta, ok := vs.Values[0].(*ast.TypeAssertExpr); ok {
					okAssertions[ta.Pos()] = true
				}
			}
		}
	}

	var results []uncheckedAssertInfo
	for pos := range allAssertions {
		if okAssertions[pos] {
			continue
		}
		p := fset.Position(pos)
		results = append(results, uncheckedAssertInfo{line: p.Line})
	}

	return results
}
