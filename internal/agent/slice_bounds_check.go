package agent

// Slice Bounds Risk Detection - Unguarded Index Access After Risky Slice Operations
//
// Problem: AI coding agents frequently index into slices returned by functions
// that may produce empty or nil results without checking the length first.
// This causes runtime panics (index out of range), which are among the most
// common crashes in production Go services.
//
// Common patterns this check catches:
//
//  1. re.FindStringSubmatch(s) -> match[1] without nil/len check
//     (FindStringSubmatch returns nil when no match is found; indexing nil panics)
//  2. strings.Split(s, sep) -> parts[1] without len check
//     (Split returns >=1 element, but parts[1] panics if the separator is absent)
//  3. strings.Fields(s) -> fields[0] without len check
//     (Fields returns an empty slice when input is all whitespace)
//  4. Same pattern for bytes.Split, filepath.SplitList, regexp.FindAll*
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no write-time detection (only crashes at runtime)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - go vet: does NOT detect unchecked slice indexing
//   - staticcheck: does NOT detect this pattern
//   - golangci-lint: no built-in linter for this
//
// Research basis:
//   - OWASP: "insufficient input validation" is a top-10 vulnerability
//   - Go blog "Errors are values": unchecked slice access is a common panic source
//   - Production postmortems: nil-slice indexing after regexp match is a
//     recurring root cause of 500 errors in Go HTTP handlers
//
// Approach: AST-based analysis. For each function, find assignments from known
// "risky slice" functions, then check if the result variable is subsequently
// indexed (with a literal index >= the function's minimum risky index) without
// a len() guard between the assignment and the index access.
// Delta-aware: only flags patterns INTRODUCED by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxSliceBoundsWarnings = 4

// riskySliceByPkg maps "pkg.Method" to the minimum literal index that is risky.
// Index 0 being risky means even [0] can panic (function may return nil/empty).
// Index 1 means [0] is always safe but [1] or higher may panic.
var riskySliceByPkg = map[string]int{
	"strings.Split":      1,
	"strings.SplitN":     1,
	"strings.Fields":     0,
	"strings.FieldsFunc": 0,
	"bytes.Split":        1,
	"bytes.Fields":       0,
	"filepath.SplitList": 1,
}

// riskySliceByMethod maps method names (matched regardless of receiver) to
// minimum risky index. Used for regexp methods where the receiver is typically
// a local variable (e.g., re.FindStringSubmatch).
var riskySliceByMethod = map[string]int{
	"FindStringSubmatch":    0,
	"FindSubmatch":          0,
	"FindAllStringSubmatch": 0,
	"FindAllString":         0,
	"FindAllSubmatch":       0,
}

// sliceBoundsRisk represents a detected unguarded slice index access.
type sliceBoundsRisk struct {
	line     int
	varName  string
	index    int
	funcName string
}

// checkSliceBoundsRisk detects indexing of slices returned by risky functions
// without a length guard. Only flags NEW instances introduced by this edit.
func checkSliceBoundsRisk(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil
	}

	newRisks := findSliceBoundsRisks(fset, newAST, strings.Split(newContent, "\n"))
	if len(newRisks) == 0 {
		return nil
	}

	var oldLines map[int]bool
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldRisks := findSliceBoundsRisks(oldFset, oldAST, strings.Split(oldContent, "\n"))
			if len(oldRisks) > 0 {
				oldLines = make(map[int]bool, len(oldRisks))
				for _, r := range oldRisks {
					oldLines[r.line] = true
				}
			}
		}
	}

	var warnings []string
	for _, r := range newRisks {
		if oldLines != nil && oldLines[r.line] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"L%d: %s[%d] used after %s() without a length check. "+
				"This function may return nil or an empty slice - indexing without "+
				"a guard will panic at runtime. Add: if len(%s) > %d { ... }",
			r.line, r.varName, r.index, r.funcName, r.varName, r.index))
		if len(warnings) >= maxSliceBoundsWarnings {
			break
		}
	}
	return warnings
}

// riskyAssign tracks a variable assigned from a risky slice-returning function.
type riskyAssign struct {
	assignLine    int
	funcName      string
	minRiskyIndex int
}

// findSliceBoundsRisks walks the AST to find unguarded index access on
// variables assigned from known risky slice-returning functions.
func findSliceBoundsRisks(fset *token.FileSet, file *ast.File, srcLines []string) []sliceBoundsRisk {
	var risks []sliceBoundsRisk

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		tracked := make(map[string]riskyAssign)

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			handleSliceBoundsNode(n, fset, srcLines, tracked, &risks)
			return true
		})
	}

	return risks
}

// handleSliceBoundsNode processes a single AST node for slice bounds risks.
// It tracks risky assignments and checks for unguarded index access.
func handleSliceBoundsNode(n ast.Node, fset *token.FileSet, srcLines []string,
	tracked map[string]riskyAssign, risks *[]sliceBoundsRisk) {

	if assign, ok := n.(*ast.AssignStmt); ok {
		funcName, minIdx := extractRiskySliceFunc(assign.Rhs)
		if len(assign.Lhs) == 0 {
			return
		}
		ident, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return
		}
		if funcName != "" {
			tracked[ident.Name] = riskyAssign{
				assignLine:    fset.Position(assign.Pos()).Line,
				funcName:      funcName,
				minRiskyIndex: minIdx,
			}
		} else {
			delete(tracked, ident.Name)
		}
		return
	}

	idx, ok := n.(*ast.IndexExpr)
	if !ok {
		return
	}

	ident, ok := idx.X.(*ast.Ident)
	if !ok {
		return
	}

	ra, found := tracked[ident.Name]
	if !found {
		return
	}

	indexVal := extractSliceIndexValue(idx.Index)
	if indexVal < 0 || indexVal < ra.minRiskyIndex {
		return
	}

	line := fset.Position(idx.Pos()).Line
	if hasLengthGuard(srcLines, ident.Name, ra.assignLine, line) {
		return
	}

	*risks = append(*risks, sliceBoundsRisk{
		line:     line,
		varName:  ident.Name,
		index:    indexVal,
		funcName: ra.funcName,
	})
}

// extractRiskySliceFunc checks if an assignment RHS is a call to a known risky
// slice-returning function. Returns the function name and minimum risky index.
func extractRiskySliceFunc(rhs []ast.Expr) (string, int) {
	if len(rhs) == 0 {
		return "", 0
	}
	call, ok := rhs[0].(*ast.CallExpr)
	if !ok {
		return "", 0
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", 0
	}
	if ident, ok := sel.X.(*ast.Ident); ok {
		qual := ident.Name + "." + sel.Sel.Name
		if minIdx, found := riskySliceByPkg[qual]; found {
			return qual, minIdx
		}
	}
	if minIdx, found := riskySliceByMethod[sel.Sel.Name]; found {
		return sel.Sel.Name, minIdx
	}
	return "", 0
}

// extractSliceIndexValue extracts a literal integer index from an index expression.
// Returns -1 for non-literal indices (e.g., arr[i]).
func extractSliceIndexValue(idx ast.Expr) int {
	lit, ok := idx.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return -1
	}
	n := 0
	for _, c := range lit.Value {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// hasLengthGuard checks if len(varName) appears in the source lines between
// the assignment line and the index access line (inclusive).
func hasLengthGuard(lines []string, varName string, fromLine, toLine int) bool {
	needle := "len(" + varName + ")"
	for i := fromLine; i <= toLine && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		if strings.Contains(lines[i], needle) {
			return true
		}
	}
	return false
}
