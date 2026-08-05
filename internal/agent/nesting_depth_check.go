package agent

// Deep Nesting Detection in Go Source Files
//
// Research basis: Cognitive Complexity (SonarSource, 2018; widely adopted in
// SonarQube by 2025) demonstrates that nesting depth is the single strongest
// predictor of code comprehension difficulty. Each additional nesting level
// increases cognitive load exponentially - a developer must hold N+1 context
// frames to understand code at depth N. Google's Go style review guidelines
// recommend keeping nesting shallow via guard clauses (early returns).
//
// AI coding agents are particularly prone to generating deeply nested code:
// they append logic inside existing if/else/for blocks rather than refactoring
// to flat structure. This check catches the pattern at write time, before it
// reaches review or CI.
//
// Competitor analysis:
//   - Claude Code: no write-time nesting detection
//   - Cursor: lint-on-save may catch via eslint complexity rules (JS only)
//   - Cline/OpenHands: no detection
//   - Devin: post-completion review may flag but not at write time
//   - SonarQube: detects in CI/analysis pipeline, NOT at write time
//   - Aider: no detection
//
// ggcode's approach: AST-based walk of control-flow statements (if, for,
// range, switch, type-switch, select). Else-if chains are treated as flat
// (same depth) since they represent a single decision level. Delta-aware:
// only flags nesting that is newly introduced or worsened by this edit.
// Zero LLM cost, runs in <1ms.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

const (
	// maxNestingDepth is the threshold above which nesting is considered
	// excessive. Warns when a function has 5+ levels of control-flow nesting
	// (i.e., depth > 4). This aligns with SonarQube's default nesting threshold.
	maxNestingDepth    = 4
	maxNestingWarnings = 3
)

// nestingViolation records a single function whose maximum control-flow
// nesting depth exceeds the recommended threshold.
type nestingViolation struct {
	funcName string
	depth    int
	line     int
}

// checkNestingDepth detects excessively deep control-flow nesting in Go
// functions introduced or worsened by this edit. Returns warning strings.
// Delta-aware: only flags nesting that is new or worse than the old content.
func checkNestingDepth(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") || strings.TrimSpace(newContent) == "" {
		return nil
	}

	newViolations := findDeepNesting(newContent)
	if len(newViolations) == 0 {
		return nil
	}

	// Build old-depth map for delta comparison.
	oldViolations := findDeepNesting(oldContent)
	oldDepths := make(map[string]int, len(oldViolations))
	for _, v := range oldViolations {
		oldDepths[v.funcName] = v.depth
	}

	// Keep only violations that are new or worse than before.
	var fresh []nestingViolation
	for _, v := range newViolations {
		oldDepth, exists := oldDepths[v.funcName]
		if exists && oldDepth >= v.depth {
			continue
		}
		fresh = append(fresh, v)
	}
	if len(fresh) == 0 {
		return nil
	}

	// Sort by depth descending, then by function name for stable output.
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].depth != fresh[j].depth {
			return fresh[i].depth > fresh[j].depth
		}
		return fresh[i].funcName < fresh[j].funcName
	})

	var warnings []string
	for i, v := range fresh {
		if i >= maxNestingWarnings {
			rem := len(fresh) - maxNestingWarnings
			warnings = append(warnings, fmt.Sprintf("... and %d more deeply nested function(s)", rem))
			break
		}
		warnings = append(warnings, formatNestingWarning(v))
	}
	return warnings
}

// formatNestingWarning produces a human-readable warning string.
func formatNestingWarning(v nestingViolation) string {
	msg := "%s has deep control-flow nesting (depth %d, recommended <= %d) - " +
		"consider extracting nested logic into helper functions or using guard clauses (early return)"
	return fmt.Sprintf(msg, v.funcName, v.depth, maxNestingDepth)
}

// findDeepNesting parses Go source and returns all functions whose maximum
// control-flow nesting depth exceeds the threshold.
func findDeepNesting(src string) []nestingViolation {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}

	var violations []nestingViolation
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		depth := computeMaxNesting(fn.Body)
		if depth > maxNestingDepth {
			violations = append(violations, nestingViolation{
				funcName: fn.Name.Name,
				depth:    depth,
				line:     fset.Position(fn.Pos()).Line,
			})
		}
	}
	return violations
}

// computeMaxNesting returns the maximum control-flow nesting depth within a
// function body block.
func computeMaxNesting(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	maxDepth := 0
	for _, stmt := range body.List {
		walkNesting(stmt, 0, &maxDepth)
	}
	return maxDepth
}

// walkNesting recursively walks statements, tracking control-flow nesting depth.
// Only control-flow statements (if, for, range, switch, type-switch, select)
// increment depth. Else-if chains are treated as flat (same depth).
func walkNesting(stmt ast.Stmt, depth int, maxDepth *int) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.IfStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkBlockStmt(s.Body, d, maxDepth)
		walkElseChain(s.Else, d, maxDepth)
	case *ast.ForStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkBlockStmt(s.Body, d, maxDepth)
	case *ast.RangeStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkBlockStmt(s.Body, d, maxDepth)
	case *ast.SwitchStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkCaseClauses(s.Body, d, maxDepth)
	case *ast.TypeSwitchStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkCaseClauses(s.Body, d, maxDepth)
	case *ast.SelectStmt:
		d := depth + 1
		if d > *maxDepth {
			*maxDepth = d
		}
		walkCommClauses(s.Body, d, maxDepth)
	case *ast.BlockStmt:
		walkBlockStmt(s, depth, maxDepth)
	case *ast.LabeledStmt:
		walkNesting(s.Stmt, depth, maxDepth)
	}
}

// walkBlockStmt walks all statements in a block at the given depth.
func walkBlockStmt(body *ast.BlockStmt, depth int, maxDepth *int) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		walkNesting(stmt, depth, maxDepth)
	}
}

// walkElseChain walks the else branch of an if statement. Else-if chains
// (elseStmt is another *ast.IfStmt) are treated as the SAME depth as the
// original if — they represent a single decision level, not deeper nesting.
func walkElseChain(elseStmt ast.Stmt, depth int, maxDepth *int) {
	for elseStmt != nil {
		if elseIf, ok := elseStmt.(*ast.IfStmt); ok {
			walkBlockStmt(elseIf.Body, depth, maxDepth)
			elseStmt = elseIf.Else
		} else if block, ok := elseStmt.(*ast.BlockStmt); ok {
			walkBlockStmt(block, depth, maxDepth)
			elseStmt = nil
		} else {
			elseStmt = nil
		}
	}
}

// walkCaseClauses walks all case clauses in a switch/type-switch body.
func walkCaseClauses(body *ast.BlockStmt, depth int, maxDepth *int) {
	if body == nil {
		return
	}
	for _, cs := range body.List {
		if cc, ok := cs.(*ast.CaseClause); ok {
			for _, bs := range cc.Body {
				walkNesting(bs, depth, maxDepth)
			}
		}
	}
}

// walkCommClauses walks all communication clauses in a select body.
func walkCommClauses(body *ast.BlockStmt, depth int, maxDepth *int) {
	if body == nil {
		return
	}
	for _, cs := range body.List {
		if cc, ok := cs.(*ast.CommClause); ok {
			for _, bs := range cc.Body {
				walkNesting(bs, depth, maxDepth)
			}
		}
	}
}
