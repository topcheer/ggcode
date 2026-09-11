package agent

// Test Isolation Detection - Global State Mutation Guard for Go Test Files
//
// Problem: AI coding agents frequently generate tests that mutate global state
// without cleanup, causing test pollution and flaky behavior in parallel test
// execution. The most common anti-patterns:
//
//  1. os.Setenv() in tests - permanently modifies process environment.
//     Other tests running in parallel see the modified env. Go 1.17+ provides
//     t.Setenv() which auto-restores after the test and also serializes the
//     test (prevents t.Parallel() interference with env state).
//
//  2. Mutating os.Args - changes the process command-line globally, affecting
//     any test that reads os.Args. There is no t.SetArgs equivalent; tests
//     must save/restore manually.
//
//  3. Redirecting os.Stdout/os.Stderr - if not restored, subsequent test
//     output goes to the wrong writer.
//
//  4. Writing to package-level variables from Test* functions - introduces
//     hidden coupling between tests. When tests run in parallel (-parallel),
//     these shared mutations cause race conditions and nondeterministic
//     failures.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on go test -race catching issues)
//   - Cursor: no detection
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf: no detection
//   - go vet: does not flag os.Setenv or global mutations in tests
//   - staticcheck: does not cover test isolation
//
// Research basis:
//   - "Testing on the Toilet" (Google): global state in tests is a top cause
//     of flaky tests in large monorepos.
//   - "The Little Manual of API Design" (Jason Sachs): mutable global state
//     makes tests non-hermetic and order-dependent.
//   - Go testing convention (testing.T.Setenv docs): "Setenv calls cannot be
//     used in parallel tests that are running" — using os.Setenv sidesteps
//     this safety mechanism entirely.
//
// ggcode's approach: AST-based, delta-aware analysis. Only flags NEW instances
// introduced by the current edit (comparing old vs new content). Advisory
// (non-blocking). Zero LLM cost. <1ms per check.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// testIsolationMaxWarnings caps the number of global-state mutation warnings per write.
const testIsolationMaxWarnings = 5

// checkTestIsolation detects global state mutations in Go test files.
// Returns a guidance string if NEW isolation violations are found.
//
// Parameters:
//   - filePath: path of the written file (only *_test.go files are checked)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkTestIsolation(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldViolations := findGlobalStateMutations(filePath, oldContent)
	newViolations := findGlobalStateMutations(filePath, newContent)

	typeCounts := computeNetCounts(newViolations, oldViolations)
	// #1502 case F: the old TOTAL-count gate (`len(new) <= len(old)`)
	// let a rewrite swap pollution kinds - old 3x os-setenv -> new 1x
	// os-stdio was "fewer violations" and passed silently. Report when ANY
	// kind has a net increase.
	anyIncrease := false
	introduced := 0
	for _, c := range typeCounts {
		if c > 0 {
			anyIncrease = true
		}
		if c > 0 {
			introduced += c
		}
	}
	if !anyIncrease {
		return ""
	}
	details := formatIsolationDetails(typeCounts)

	if len(details) == 0 {
		return ""
	}

	sort.Strings(details)
	if len(details) > testIsolationMaxWarnings {
		details = details[:testIsolationMaxWarnings]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Test isolation risk: %d new global-state mutation(s) detected in test code. "+
		"Mutating global state makes tests non-hermetic, order-dependent, and flaky under -parallel.\n", introduced))
	for _, d := range details {
		b.WriteString("  - ")
		b.WriteString(d)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// computeNetCounts returns per-kind net counts (new minus old).
func computeNetCounts(newViolations, oldViolations []globalStateMutation) map[string]int {
	typeCounts := map[string]int{}
	for _, v := range newViolations {
		typeCounts[v.kind]++
	}
	for _, v := range oldViolations {
		typeCounts[v.kind]--
	}
	return typeCounts
}

// formatIsolationDetails converts net counts into human-readable detail strings.
func formatIsolationDetails(typeCounts map[string]int) []string {
	kindMessages := map[string]string{
		"os-setenv":  "os.Setenv() call(s) - use t.Setenv() which auto-restores env after the test and prevents parallel env races",
		"os-args":    "os.Args mutation(s) - save/restore with defer to prevent test pollution",
		"os-stdio":   "os.Stdout/Stderr mutation(s) - restore with defer to prevent output capture issues in other tests",
		"global-var": "package-level variable mutation(s) from test function(s) - use local variables or t.Cleanup to restore",
	}
	var details []string
	for kind, count := range typeCounts {
		if count <= 0 {
			continue
		}
		if msg, ok := kindMessages[kind]; ok {
			details = append(details, fmt.Sprintf("%d %s", count, msg))
		}
	}
	return details
}

// globalStateMutation records a single global state mutation in a test function.
type globalStateMutation struct {
	kind string
	line int
}

// findGlobalStateMutations parses Go test source and returns all global state
// mutations found inside Test*/Benchmark*/Example* functions.
func findGlobalStateMutations(filename, src string) []globalStateMutation {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// #1502 case B: package vars are almost always declared in the SOURCE
	// file under test, not the test file itself - collecting only the test
	// file's own VAR decls (plus the single-file Obj==nil resolution quirk)
	// made global-var detection near-inert in real projects. Collect the
	// sibling non-test .go files' package-level vars too.
	packageVars := collectPackageVarNames(file)
	for n := range collectSiblingPackageVarNames(filename) {
		packageVars[n] = true
	}

	var mutations []globalStateMutation
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isTestFunction(fn.Name.Name) {
			continue
		}
		// #1502 case C: TestMain has no *testing.T - t.Setenv is unusable
		// there and the Go docs require env setup BEFORE m.Run via plain
		// os.Setenv. Advising otherwise makes the agent emit code that does
		// not compile. Exempt TestMain entirely from this check.
		if fn.Name.Name == "TestMain" {
			continue
		}
		mutations = append(mutations, inspectTestFuncBody(fn.Body, packageVars, fset)...)
	}
	return mutations
}

// collectPackageVarNames returns a set of package-level variable names.
func collectPackageVarNames(file *ast.File) map[string]bool {
	packageVars := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "" && name.Name != "_" {
					packageVars[name.Name] = true
				}
			}
		}
	}
	return packageVars
}

// inspectTestFuncBody walks a test function body and collects global state mutations.
func inspectTestFuncBody(body *ast.BlockStmt, packageVars map[string]bool, fset *token.FileSet) []globalStateMutation {
	// #1502 case D: the SNAPSHOT pattern (`old := X; X = v; defer func()
	// { X = old }()`) is fully hermetic, but both the forward assignment
	// and the restore count as mutations (net +2) - a perfectly restoring
	// test was branded "global-state mutations" and the guidance pushed
	// the agent to dismantle correct restoration code. Collect the names
	// assigned inside defer/cleanup closures FIRST and treat their writes
	// as restores.
	restores := collectRestoredVars(body)
	var mutations []globalStateMutation
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if m := detectOSSetenvCall(node, fset); m != nil {
				mutations = append(mutations, *m)
			}
		case *ast.AssignStmt:
			mutations = append(mutations, detectAssignMutations(node, packageVars, restores, fset)...)
		}
		return true
	})
	return mutations
}

// collectRestoredVars returns the variable names assigned inside function
// literals reached via defer or t.Cleanup (the restore half of the snapshot
// pattern) plus any variable BOTH written outside and restored inside.
func collectRestoredVars(body *ast.BlockStmt) map[string]bool {
	restored := map[string]bool{}
	var walkDefer bool
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.DeferStmt:
			walkDefer = true
			ast.Inspect(node.Call, func(m ast.Node) bool {
				if fl, ok := m.(*ast.FuncLit); ok {
					collectAssignedNames(fl.Body, restored)
				}
				return true
			})
			walkDefer = false
		case *ast.CallExpr:
			if !walkDefer {
				if se, ok := node.Fun.(*ast.SelectorExpr); ok && se.Sel != nil && se.Sel.Name == "Cleanup" {
					for _, arg := range node.Args {
						ast.Inspect(arg, func(m ast.Node) bool {
							if fl, ok := m.(*ast.FuncLit); ok {
								collectAssignedNames(fl.Body, restored)
							}
							return true
						})
					}
				}
			}
		}
		return true
	})
	if len(restored) == 0 {
		return nil
	}
	// Only names also assigned OUTSIDE the closures count as snapshotted
	// (restore-without-write is dead code; write-without-restore must stay
	// flagged).
	snapshotted := map[string]bool{}
	var outside func(n ast.Node) bool
	outside = func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // skip closure interiors; handled above
		}
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && restored[id.Name] {
					snapshotted[id.Name] = true
				}
			}
		}
		return true
	}
	ast.Inspect(body, outside)
	return snapshotted
}

// collectAssignedNames records simple identifier LHS names in a body.
func collectAssignedNames(body ast.Node, into map[string]bool) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "" && id.Name != "_" {
					into[id.Name] = true
				}
			}
		}
		return true
	})
}

// collectSiblingPackageVarNames parses sibling non-test .go files in the
// same directory and collects their package-level variable names (#1502 B).
func collectSiblingPackageVarNames(testFilePath string) map[string]bool {
	names := map[string]bool{}
	dir := filepath.Dir(testFilePath)
	if dir == "" || dir == "." {
		return names
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil || f == nil {
			continue
		}
		for name := range collectPackageVarNames(f) {
			names[name] = true
		}
	}
	return names
}

// detectOSSetenvCall checks if a CallExpr is os.Setenv and returns a mutation if so.
func detectOSSetenvCall(node *ast.CallExpr, fset *token.FileSet) *globalStateMutation {
	se, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := se.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if (pkgIdent.Name == "os" || pkgIdent.Name == "OS") && se.Sel.Name == "Setenv" {
		return &globalStateMutation{
			kind: "os-setenv",
			line: fset.Position(node.Pos()).Line,
		}
	}
	return nil
}

// detectAssignMutations inspects an AssignStmt's LHS for global state mutations.
// restores (#1502 D): names in the snapshot-restore set are skipped.
func detectAssignMutations(node *ast.AssignStmt, packageVars map[string]bool, restores map[string]bool, fset *token.FileSet) []globalStateMutation {
	var mutations []globalStateMutation
	for _, lhs := range node.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && restores != nil && restores[id.Name] {
			continue
		}
		if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel != nil && restores != nil && restores["os."+sel.Sel.Name] {
			continue
		}
		if m := detectOSGlobalAssignment(lhs, fset); m != nil {
			mutations = append(mutations, *m)
			continue
		}
		if m := detectPackageVarAssignment(lhs, packageVars, fset); m != nil {
			mutations = append(mutations, *m)
		}
	}
	return mutations
}

// detectOSGlobalAssignment checks if an LHS expression is os.Args, os.Stdout, or os.Stderr.
func detectOSGlobalAssignment(lhs ast.Expr, fset *token.FileSet) *globalStateMutation {
	sel, ok := lhs.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if pkgIdent.Name != "os" && pkgIdent.Name != "OS" {
		return nil
	}
	field := sel.Sel.Name
	switch field {
	case "Args":
		return &globalStateMutation{kind: "os-args", line: fset.Position(lhs.Pos()).Line}
	case "Stdout", "Stderr":
		return &globalStateMutation{kind: "os-stdio", line: fset.Position(lhs.Pos()).Line}
	}
	return nil
}

// detectPackageVarAssignment checks if an LHS expression writes to a package-level variable.
func detectPackageVarAssignment(lhs ast.Expr, packageVars map[string]bool, fset *token.FileSet) *globalStateMutation {
	ident, ok := lhs.(*ast.Ident)
	if !ok || !packageVars[ident.Name] {
		return nil
	}
	// #1502 case B: ident.Obj is ALWAYS nil for names resolved outside the
	// single-file parse (i.e. declared in a sibling source file) - the old
	// `ident.Obj == nil` + `Obj.Kind == Var` gate skipped exactly the
	// cross-file mutations that matter most. packageVars membership (now
	// sibling-aware) is the authority.
	return &globalStateMutation{kind: "global-var", line: fset.Position(lhs.Pos()).Line}
}

// isTestFunction returns true if the function name matches Go test conventions:
// Test*, Benchmark*, Example*, Fuzz*.
func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}
