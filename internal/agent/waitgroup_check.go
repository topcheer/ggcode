package agent

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
)

const maxWGMisuseWarnings = 3

type wgMisuseInfo struct {
	pattern string
	count   int
}

type wgStats struct {
	addTotal  int
	doneBare  int
	doneDefer int
	waitTotal int
	addInGo   int
}

func checkWaitGroupMisuse(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findWaitGroupMisuse(oldContent)
	newIssues := findWaitGroupMisuse(newContent)

	oldSet := make(map[string]bool)
	for _, iss := range oldIssues {
		oldSet[iss.pattern] = true
	}
	var newOnly []wgMisuseInfo
	for _, iss := range newIssues {
		if !oldSet[iss.pattern] {
			newOnly = append(newOnly, iss)
		}
	}
	if len(newOnly) == 0 {
		return nil
	}

	var warnings []string
	seen := make(map[string]bool)
	for _, issue := range newOnly {
		if seen[issue.pattern] {
			continue
		}
		seen[issue.pattern] = true
		if issue.count > 1 {

			warnings = append(warnings, fmt.Sprintf("%s (%d occurrences)", issue.pattern, issue.count))
		} else {
			warnings = append(warnings, issue.pattern)
		}
		if len(warnings) >= maxWGMisuseWarnings {
			break
		}
	}
	return warnings
}

func findWaitGroupMisuse(src string) []wgMisuseInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	if !strings.Contains(src, "WaitGroup") {
		return nil
	}

	file, _, err := parseGoSource("", src, 0)
	if err != nil {
		return nil
	}

	var issues []wgMisuseInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		issues = append(issues, analyzeWGFunc(fn)...)
	}
	return issues
}

func wgParamType(fn *ast.FuncDecl) bool {

	if fn.Recv != nil {

		if recvFieldWGDone(fn) {
			return true
		}
	}
	if fn.Type == nil || fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {

		if star, ok := p.Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "WaitGroup") {
				return true
			}
			if id, ok := star.X.(*ast.Ident); ok && strings.HasSuffix(id.Name, "WaitGroup") {
				return true
			}
		}
		if sel, ok := p.Type.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "WaitGroup") {
			return true
		}
	}
	return false
}

func recvFieldWGDone(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || fn.Body == nil {
		return false
	}
	recvNames := make(map[string]bool)
	for _, p := range fn.Recv.List {
		for _, n := range p.Names {
			recvNames[n.Name] = true
		}
	}
	if len(recvNames) == 0 {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Done" {
			return true
		}
		field, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := field.X.(*ast.Ident); ok && recvNames[id.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func analyzeWGFunc(fn *ast.FuncDecl) []wgMisuseInfo {
	body := fn.Body
	stats := collectWGStats(body)
	var issues []wgMisuseInfo

	if stats.doneBare > 0 && stats.doneDefer == 0 {
		issues = append(issues, wgMisuseInfo{
			pattern: "wg.Done() is called without defer. " +
				"Any early return or panic between Add() and Done() will skip " +
				"the decrement, causing wg.Wait() to hang forever. " +
				"Use 'defer wg.Done()' immediately after wg.Add(1).",
			count: stats.doneBare,
		})
	}

	donePresent := stats.doneBare > 0 || stats.doneDefer > 0
	if donePresent && stats.addTotal == 0 && !wgParamType(fn) {
		issues = append(issues, wgMisuseInfo{
			pattern: "WaitGroup Done() is called but Add() is never called " +
				"in this function. Without Add(1) the counter stays at 0: " +
				"Wait() returns immediately (race) and Done() panics " +
				"('sync: negative WaitGroup counter'). Ensure Add(1) is called " +
				"before spawning each goroutine.",
		})
	}

	wgConfirmed := donePresent || stats.waitTotal > 0
	allAddInGo := stats.addInGo > 0 && stats.addTotal == stats.addInGo
	if wgConfirmed && allAddInGo {
		issues = append(issues, wgMisuseInfo{
			pattern: "wg.Add(1) is called inside a goroutine body (go func). " +
				"This is a race condition: the goroutine may not execute before " +
				"Wait() is called, so Wait() returns prematurely. " +
				"Move wg.Add(1) BEFORE the 'go' statement.",
		})
	}

	return issues
}

func collectWGStats(body *ast.BlockStmt) wgStats {
	var s wgStats
	var goStmts []*ast.GoStmt

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			goStmts = append(goStmts, node)
		case *ast.DeferStmt:
			switch wgMethodName(node.Call) {
			case "Done":
				s.doneDefer++
			case "Add":
				s.addTotal++
			}
		case *ast.ExprStmt:
			if call, ok := node.X.(*ast.CallExpr); ok {
				switch wgMethodName(call) {
				case "Add":
					s.addTotal++
				case "Done":
					s.doneBare++
				case "Wait":
					s.waitTotal++
				}
			}
		}
		return true
	})

	for _, gs := range goStmts {
		if goroutineHasWGAdd(gs) {
			s.addInGo++
		}
	}

	return s
}

func wgMethodName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

func goroutineHasWGAdd(gs *ast.GoStmt) bool {
	if gs == nil || gs.Call == nil {
		return false
	}
	var found bool
	ast.Inspect(gs.Call, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if wgMethodName(call) == "Add" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
