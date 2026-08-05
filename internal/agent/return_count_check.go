package agent

// Excessive Return Statements Check (SonarQube S114 / Clean Code "Single Responsibility")
//
// Research basis: SonarQube rule S114 (max-return-statements), CodeClimate
// "Too Many Return Statements", and structured programming principles all flag
// functions with too many return points. Functions with 6+ returns are harder
// to reason about: resource cleanup may be missed, invariants may not hold at
// every exit, and the control flow becomes difficult to trace mentally.
//
// This check fills a gap in ggcode's quality intelligence: while complexity_gate
// catches high cyclomatic complexity, it does NOT directly flag return-count
// bloat. A function can have moderate complexity but 8 return statements that
// make it error-prone for maintenance. These are distinct code smells.
//
// Design:
//   - Zero-LLM-cost: deterministic AST analysis (go/ast)
//   - Delta-aware: only flags NEW instances introduced by this edit
//   - Threshold: 6+ return statements (SonarQube default)
//   - Skips test functions (TestXxx) which legitimately use table-driven returns
//   - Skips anonymous closures (common for early-exit patterns)
//   - Capped at 3 warnings per file

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const (
	// returnCountThreshold is the maximum number of return statements before flagging.
	returnCountThreshold = 6

	// maxReturnCountWarnings caps warnings per file.
	maxReturnCountWarnings = 3
)

type returnCountInstance struct {
	funcName string
	pos      token.Position
	count    int
}

func checkExcessiveReturns(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := countExcessiveReturns(oldContent)
	newInstances := findExcessiveReturns(newContent)
	if len(newInstances) <= oldCount {
		return nil
	}

	var warnings []string
	for i := oldCount; i < len(newInstances) && len(warnings) < maxReturnCountWarnings; i++ {
		inst := newInstances[i]
		warnings = append(warnings, fmt.Sprintf(
			"Too many return statements: function `%s` at %s has %d return statements. "+
				"Functions with %d+ returns are harder to maintain and reason about - "+
				"consider extracting helper functions or restructuring control flow. "+
				"(SonarQube S114, Structured Programming)",
			inst.funcName, inst.pos.String(), inst.count,
			returnCountThreshold,
		))
	}

	if len(newInstances) > oldCount+maxReturnCountWarnings {
		warnings = append(warnings, fmt.Sprintf(
			"...and %d more function(s) with %d+ return statements",
			len(newInstances)-oldCount-maxReturnCountWarnings, returnCountThreshold,
		))
	}

	return warnings
}

func countExcessiveReturns(src string) int {
	return len(findExcessiveReturns(src))
}

func findExcessiveReturns(src string) []returnCountInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []returnCountInstance

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		// Skip test functions - table-driven tests legitimately use many returns.
		if strings.HasPrefix(fn.Name.Name, "Test") || strings.HasPrefix(fn.Name.Name, "Benchmark") {
			return true
		}

		if fn.Body == nil {
			return true
		}

		count := countReturns(fn.Body)
		if count >= returnCountThreshold {
			pos := fset.Position(fn.Pos())
			instances = append(instances, returnCountInstance{
				funcName: fn.Name.Name,
				pos:      pos,
				count:    count,
			})
		}

		return true
	})

	return instances
}

// countReturns counts all return statements within an AST node's subtree.
func countReturns(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			count++
		}
		// Don't descend into nested function literals - their returns
		// belong to the inner function, not this one.
		if _, ok := n.(*ast.FuncLit); ok && n != node {
			return false
		}
		return true
	})
	return count
}
