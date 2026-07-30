package agent

// test_impact_ast.go implements AST-based function-level test coverage
// analysis. This goes beyond file-level coverage (test_impact.go) to identify
// specific exported functions and methods that lack corresponding test
// functions — directly mirroring GitHub Copilot's "Generate Tests" feature
// which analyzes function signatures to suggest test generation.
//
// Competitor mapping:
//   - GitHub Copilot: AST-based function signature analysis for test generation
//   - Cursor: identifies specific functions needing test updates after edits
//   - JetBrains AI: per-function coverage gap reporting
//
// Implementation uses only the Go standard library (go/parser, go/ast) — zero
// external dependencies, no network calls, no I/O beyond reading source files.
// Parsing a single Go file takes microseconds, making this suitable for the
// non-blocking post-edit hint path.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// exportedFuncInfo describes an exported function or method found via AST
// parsing of a Go source file.
type exportedFuncInfo struct {
	DisplayName string // "Foo" for functions, "Bar_Method" for methods
	IsMethod    bool
}

// parseExportedFuncs reads a Go source file and returns its exported functions
// and methods. Returns nil on parse error or when no exported functions exist.
// Skips special functions (init, main, Test*).
func parseExportedFuncs(filePath string) []exportedFuncInfo {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil
	}

	var funcs []exportedFuncInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		// Skip unexported, init, main, and test-prefixed functions.
		if !fn.Name.IsExported() {
			continue
		}
		if name == "Test" || strings.HasPrefix(name, "Test") {
			// These are test helper functions that happen to be exported;
			// they don't need their own tests.
			continue
		}

		var display string
		isMethod := false
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			isMethod = true
			recvType := receiverTypeName(fn.Recv.List[0].Type)
			if recvType != "" {
				display = recvType + "_" + name
			} else {
				display = name
			}
		} else {
			display = name
		}
		funcs = append(funcs, exportedFuncInfo{
			DisplayName: display,
			IsMethod:    isMethod,
		})
	}
	return funcs
}

// receiverTypeName extracts the type name from a receiver expression,
// handling both value receivers (Bar) and pointer receivers (*Bar).
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// parseTestFuncNames reads a Go test file and returns the set of function names
// that start with "Test" (the Go testing convention). Returns nil on parse
// error or when no test functions exist.
func parseTestFuncNames(filePath string) map[string]bool {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil
	}
	names := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "Test") {
			names[fn.Name.Name] = true
		}
	}
	return names
}

// untestedExportedFuncs returns the display names of exported functions/methods
// in a Go source file that have no corresponding test function in its _test.go
// file. This is the function-level granularity that competitors provide: instead
// of saying "foo.go has no tests", it says "foo.go: Process, Validate, Handler
// are untested".
//
// Test matching convention:
//   - Function Foo → TestFoo
//   - Method (*Bar).Method → TestBar_Method or TestBarMethod
//
// Returns nil if the file has no exported functions or can't be parsed.
func untestedExportedFuncs(workingDir, goFile string) []string {
	abs := goFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, goFile)
	}
	funcs := parseExportedFuncs(abs)
	if len(funcs) == 0 {
		return nil
	}

	// Find the corresponding test file.
	base := filepath.Base(goFile)
	srcName := strings.TrimSuffix(base, ".go")
	testFile := filepath.Join(filepath.Dir(abs), srcName+"_test.go")
	testFuncs := parseTestFuncNames(testFile)
	if len(testFuncs) == 0 {
		// No test file at all — every exported function is untested.
		result := make([]string, 0, len(funcs))
		for _, f := range funcs {
			result = append(result, f.DisplayName)
		}
		return result
	}

	var untested []string
	for _, f := range funcs {
		// Primary convention: TestFuncName or TestType_Method
		primary := "Test" + f.DisplayName
		if testFuncs[primary] {
			continue
		}
		// Fallback convention: TestTypeMethod (no underscore)
		if f.IsMethod {
			parts := strings.SplitN(f.DisplayName, "_", 2)
			if len(parts) == 2 {
				altName := "Test" + parts[0] + parts[1]
				if testFuncs[altName] {
					continue
				}
			}
		}
		untested = append(untested, f.DisplayName)
	}
	return untested
}

// funcLevelCoverageGaps analyzes changed Go files and returns per-file lists
// of untested exported functions. The result is capped (maxFiles files,
// maxFuncsPerFile functions per file) to avoid bloating the hint.
type funcCoverageGap struct {
	File  string
	Funcs []string
}

func funcLevelCoverageGaps(workingDir string, maxFiles, maxFuncsPerFile int) []funcCoverageGap {
	files := changedGoFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}

	var gaps []funcCoverageGap
	for _, f := range files {
		untested := untestedExportedFuncs(workingDir, f)
		if len(untested) == 0 {
			continue
		}
		if len(untested) > maxFuncsPerFile {
			untested = untested[:maxFuncsPerFile]
		}
		gaps = append(gaps, funcCoverageGap{
			File:  filepath.Base(f),
			Funcs: untested,
		})
		if len(gaps) >= maxFiles {
			break
		}
	}
	return gaps
}
