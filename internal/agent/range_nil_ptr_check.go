package agent

// Range Over Nil Pointer Detection in Go Code
//
// Problem: AI coding agents sometimes produce code that ranges over a pointer
// to a slice or map without first checking if the pointer is nil. If the
// pointer is nil, ranging over it causes a nil pointer dereference panic at
// runtime -- one of the most common Go crashes.
//
// Example bug:
//
//	func process(items *[]Item) {
//	    for _, item := range *items {   // panics if items == nil
//	        handle(item)
//	    }
//	}
//
// Safe version:
//
//	func process(items *[]Item) {
//	    if items == nil {
//	        return
//	    }
//	    for _, item := range *items {
//	        handle(item)
//	    }
//	}
//
// This check also detects ranging over a pointer-to-slice/map RETURNED from a
// function call (e.g., for _, v := range *getPtr() {}), which is even more
// dangerous since the nil-ness cannot be checked beforehand without a
// temporary variable.
//
// Nil-guard awareness: a range over *x is NOT flagged when the enclosing
// function contains a nil guard for x -- either an `if x == nil {...}` check
// appearing before the range statement, or the range statement being nested
// inside an `if x != nil {...}` block.
//
// Delta-aware: only flags instances introduced by this edit (fingerprinted by
// variable name + trimmed line text), so pre-existing instances are not
// re-reported on every edit.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no write-time detection
//   - go vet: does not flag this pattern
//   - staticcheck (SA5011): detects some nil pointer dereferences but is
//     conservative on range expressions
//
// This check is purely AST-based (zero LLM cost) and runs as a post-write
// integrity gate.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const maxRangeNilPtrWarnings = 6

// rnpWarning is one range-nil-pointer finding with its delta fingerprint.
type rnpWarning struct {
	text        string
	fingerprint string
}

// rnpNilGuard records a nil comparison (`x == nil` / `x != nil` / `nil == x`)
// found in an if statement within the same function.
type rnpNilGuard struct {
	negated    bool      // true for `!= nil`, false for `== nil`
	ifPos      token.Pos // position of the enclosing if statement
	thenEnd    token.Pos // end of the THEN block (excludes else) (#271)
	ifEnd      token.Pos // end of the enclosing if statement
	hasElse    bool      // the if has an else block (#271)
	terminates bool      // body ends in return/panic/continue-style exit (#265)
}

// checkRangeNilPtr detects range statements that dereference a pointer
// without a preceding nil guard, and only reports instances newly introduced
// relative to oldContent.
func checkRangeNilPtr(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	newWarnings := rnpScanContent(filePath, newContent)

	// Delta suppression: skip instances whose fingerprint (variable name +
	// trimmed line text) already exists in the old content.
	if len(newWarnings) > 0 && strings.TrimSpace(oldContent) != "" {
		oldSet := rnpScanFingerprints(filePath, oldContent)
		kept := newWarnings[:0]
		for _, w := range newWarnings {
			if !oldSet[w.fingerprint] {
				kept = append(kept, w)
			}
		}
		newWarnings = kept
	}

	if len(newWarnings) == 0 {
		return ""
	}

	msgs := make([]string, 0, len(newWarnings))
	for _, w := range newWarnings {
		msgs = append(msgs, w.text)
	}
	if len(msgs) > maxRangeNilPtrWarnings {
		remaining := len(msgs) - maxRangeNilPtrWarnings
		msgs = msgs[:maxRangeNilPtrWarnings]
		msgs = append(msgs, fmt.Sprintf("...and %d more range-nil-pointer warning(s)", remaining))
	}
	return strings.Join(msgs, "\n")
}

// rnpScanFingerprints returns the set of delta fingerprints for a content.
func rnpScanFingerprints(filePath, content string) map[string]bool {
	warnings := rnpScanContent(filePath, content)
	set := make(map[string]bool, len(warnings))
	for _, w := range warnings {
		set[w.fingerprint] = true
	}
	return set
}

// rnpScanContent parses content and returns all unguarded range-nil-pointer
// warnings found in it.
func rnpScanContent(filePath, content string) []rnpWarning {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}
	lines := strings.Split(content, "\n")

	var warnings []rnpWarning
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		guards := rnpCollectNilGuards(fd.Body)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			rs, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			msg, varName := rnpCheckStmt(rs, fset, guards)
			if msg == "" {
				return true
			}
			lineText := rnpLineText(lines, fset.Position(rs.X.Pos()).Line)
			warnings = append(warnings, rnpWarning{
				text:        msg,
				fingerprint: varName + "|" + lineText,
			})
			return true
		})
	}
	return warnings
}

// rnpLineText returns the trimmed text of a 1-based line number.
func rnpLineText(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

// rnpCollectNilGuards collects nil-comparison guards per variable name within
// a function body. Both `== nil` and `!= nil` forms (either operand order)
// are collected; the negated flag distinguishes them later.
func rnpCollectNilGuards(body *ast.BlockStmt) map[string][]rnpNilGuard {
	guards := make(map[string][]rnpNilGuard)
	ast.Inspect(body, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		name, negated, found := rnpNilCompareIdent(ifStmt.Cond)
		if !found {
			return true
		}
		_, isPlainElse := ifStmt.Else.(*ast.BlockStmt)
		guards[name] = append(guards[name], rnpNilGuard{
			negated:    negated,
			ifPos:      ifStmt.Pos(),
			thenEnd:    ifStmt.Body.End(),
			ifEnd:      ifStmt.End(),
			hasElse:    isPlainElse,
			terminates: ifBodyTerminates(ifStmt.Body), // reused from nil_deref_check (#265)
		})
		return true
	})
	return guards
}

// rnpNilCompareIdent checks whether cond is `x == nil`, `nil == x`,
// `x != nil`, or `nil != x` for a plain identifier or a field-selector chain
// rooted at one (x, p.Field, a.b.c), returning the dotted variable name and
// whether the comparison is negated (!=).
func rnpNilCompareIdent(cond ast.Expr) (name string, negated, found bool) {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return "", false, false
	}
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return "", false, false
	}
	negated = bin.Op == token.NEQ
	if rnpIsNilIdent(bin.Y) {
		if n := rnpDottedName(bin.X); n != "" {
			return n, negated, true
		}
	}
	if rnpIsNilIdent(bin.X) {
		if n := rnpDottedName(bin.Y); n != "" {
			return n, negated, true
		}
	}
	return "", false, false
}

// rnpDottedName returns the dotted path for an identifier or a chain of
// field selectors rooted at an identifier (x, p.Field, a.b.c). Empty for
// anything else (calls, index, type assertions). Selector guards are the
// most idiomatic Go nil-protection form (#1483): `if p.Field != nil` must
// guard `for range *p.Field` the same way plain idents do.
func rnpDottedName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		base := rnpDottedName(v.X)
		if base == "" {
			return ""
		}
		return base + "." + v.Sel.Name
	}
	return ""
}

// rnpIsNilIdent reports whether the expression is the nil identifier.
func rnpIsNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// rnpCheckStmt inspects a single range statement for pointer dereference in
// the range expression without a nil guard. guards maps variable names to
// their nil-comparison guards within the enclosing function. Returns the
// warning message and the dereferenced variable name ("" for non-variable
// expressions, which cannot be guarded).
func rnpCheckStmt(rs *ast.RangeStmt, fset *token.FileSet, guards map[string][]rnpNilGuard) (string, string) {
	star, ok := rs.X.(*ast.StarExpr)
	if !ok {
		return "", ""
	}

	varName := rnpExprName(star.X)
	pos := fset.Position(star.Pos())

	if varName != "" && rnpHasNilGuard(varName, rs.Pos(), guards) {
		return "", ""
	}

	if varName == "" {
		return fmt.Sprintf("%s: range over pointer dereference of a non-variable expression -- "+
			"if the pointer is nil this will panic. Store in a variable and check for nil first.",
			rnpFormatPos(pos)), ""
	}

	return fmt.Sprintf("%s: range over *%s without nil check -- if %s is nil this will "+
		"panic with nil pointer dereference. Add `if %s == nil { return }` before the loop.",
		rnpFormatPos(pos), varName, varName, varName), varName
}

// rnpHasNilGuard reports whether the variable is nil-guarded relative to the
// range statement: either an `if x == nil {...}` check appears earlier in the
// same function, or the range statement is nested inside an
// `if x != nil {...}` block.
func rnpHasNilGuard(varName string, rangePos token.Pos, guards map[string][]rnpNilGuard) bool {
	for _, g := range guards[varName] {
		if g.ifPos < rangePos && rangePos < g.thenEnd {
			// Range statement is inside the THEN block: guarded only when the
			// condition is `x != nil` (proves x non-nil in that block).
			return g.negated
		}
		if g.hasElse && g.thenEnd <= rangePos && rangePos < g.ifEnd {
			// Range statement is inside the ELSE block (#271): the condition is
			// inverted there. `if x != nil` else proves x IS nil (unguarded);
			// `if x == nil` else proves x is non-nil (guarded). Only applied when
			// Else is a plain BlockStmt: for else-if chains the nested if gets its
			// own guard record via recursive Inspect, so we conservatively fall
			// through and let that record decide.
			return !g.negated
		}
		if g.ifPos < rangePos && rangePos < g.ifEnd {
			// Range is inside the if's extent but not in a recognized then/else
			// plain block (e.g. else-if chain region) -- rely on nested guard
			// records instead; keep scanning other guards.
			continue
		}
		if !g.negated && g.terminates && g.ifEnd < rangePos {
			// `if x == nil { return/panic }` block fully BEFORE the range.
			// Only a TERMINATING body guarantees the range is unreachable
			// when x is nil (#265): `if x == nil { log }` falls through and
			// the range still panics.
			return true
		}
	}
	return false
}

// rnpExprName extracts the variable name from a dereferenced expression.
// Returns a dotted path for identifiers and field-selector chains (x,
// p.Field) matching the guard keys produced by rnpNilCompareIdent, and
// empty for other complex expressions (calls, index).
func rnpExprName(expr ast.Expr) string {
	return rnpDottedName(expr)
}

// rnpFormatPos formats a token.Position for display.
func rnpFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
