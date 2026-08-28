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
	recvType string // receiver type text ("*Server" etc); empty for plain funcs (#1193)
	pos      token.Position
	count    int
}

// rcFingerprint keys an instance for delta suppression (fix #157, #1193).
// The receiver TYPE is part of the key: (s *Server) handle and (c *Client)
// handle are different functions whose identical bare names must not collide,
// silently absorbing a NEW same-named method as pre-existing (#1193, same
// family as the param_count_check #1149 fix).
func (i returnCountInstance) rcFingerprint() string {
	return i.recvType + "|" + i.funcName
}

func checkExcessiveReturns(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// #1193: the Test*/Benchmark* name exemption applies only to _test.go
	// files - go test only ever compiles such functions from _test.go, so a
	// Test-prefixed business function in production code (TestConnection, ...)
	// must still be checked. Mirrors the param_count_check #1187 fix.
	isTestFile := strings.HasSuffix(filePath, "_test.go")

	// Delta-aware: only flag newly introduced instances (fix #142).
	newInstances := findExcessiveReturns(newContent, isTestFile)
	if len(newInstances) == 0 {
		return nil
	}

	// Count-based (multiset) delta suppression: two same-fingerprint old
	// instances must each consume one match, otherwise a NEW (N+1)-th instance
	// is silently absorbed (#1193, same family as param_count_check #1149).
	var oldCounts map[string]int
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findExcessiveReturns(oldContent, isTestFile) {
			if oldCounts == nil {
				oldCounts = make(map[string]int)
			}
			// Fingerprint on receiver type + function name, not position: line
			// shifts above a function must not re-flag pre-existing issues
			// (fix #157, #1193).
			oldCounts[iss.rcFingerprint()]++
		}
	}

	var warnings []string
	newCount := 0
	for _, inst := range newInstances {
		if oldCounts[inst.rcFingerprint()] > 0 {
			oldCounts[inst.rcFingerprint()]--
			continue
		}
		newCount++
		if len(warnings) < maxReturnCountWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"Too many return statements: function `%s` at %s has %d return statements. "+
					"Functions with %d+ returns are harder to maintain and reason about - "+
					"consider extracting helper functions or restructuring control flow. "+
					"(SonarQube S114, Structured Programming)",
				inst.funcName, inst.pos.String(), inst.count,
				returnCountThreshold,
			))
		}
	}

	if newCount > maxReturnCountWarnings {
		warnings = append(warnings, fmt.Sprintf(
			"...and %d more function(s) with %d+ return statements",
			newCount-maxReturnCountWarnings, returnCountThreshold,
		))
	}

	return warnings
}

func countExcessiveReturns(src string, isTestFile bool) int {
	return len(findExcessiveReturns(src, isTestFile))
}

func findExcessiveReturns(src string, isTestFile bool) []returnCountInstance {
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

		// #1193: skip test functions only in _test.go files - table-driven
		// tests legitimately use many returns, but a Test-prefixed function
		// in production code is regular business logic and stays checked.
		if isTestFile && isTestOrBenchFunction(fn.Name.Name) {
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
				recvType: recvTypeText(fn.Recv), // #1193: receiver type, not variable name
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
