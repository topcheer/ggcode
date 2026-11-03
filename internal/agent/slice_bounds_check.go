package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxSliceBoundsWarnings = 4

var riskySliceByPkg = map[string]int{
	"strings.Split":      1,
	"strings.SplitN":     1,
	"strings.Fields":     0,
	"strings.FieldsFunc": 0,
	"bytes.Split":        1,
	"bytes.Fields":       0,
	"filepath.SplitList": 1,
}

var riskySliceByMethod = map[string]int{
	"FindStringSubmatch":    0,
	"FindSubmatch":          0,
	"FindAllStringSubmatch": 0,
	"FindAllString":         0,
	"FindAllSubmatch":       0,
}

type sliceBoundsRisk struct {
	line        int
	varName     string
	index       int
	funcName    string
	fingerprint string
}

func checkSliceBoundsRisk(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return nil
	}

	newRisks := findSliceBoundsRisks(fset, newAST, strings.Split(newContent, "\n"))
	if len(newRisks) == 0 {
		return nil
	}

	var oldFPs map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldRisks := findSliceBoundsRisks(oldFset, oldAST, strings.Split(oldContent, "\n"))
			if len(oldRisks) > 0 {
				oldFPs = make(map[string]bool, len(oldRisks))
				for _, r := range oldRisks {
					oldFPs[r.fingerprint] = true
				}
			}
		}
	}

	var warnings []string
	for _, r := range newRisks {
		if oldFPs != nil && oldFPs[r.fingerprint] {
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

type riskyAssign struct {
	assignLine    int
	funcName      string
	minRiskyIndex int
}

func findSliceBoundsRisks(fset *token.FileSet, file *ast.File, srcLines []string) []sliceBoundsRisk {
	var risks []sliceBoundsRisk

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		enclosingFunc := ""
		if fn.Name != nil {
			enclosingFunc = fn.Name.Name
		}
		tracked := make(map[string]riskyAssign)

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			handleSliceBoundsNode(n, fset, srcLines, tracked, &risks, enclosingFunc)
			return true
		})
	}

	return risks
}

func handleSliceBoundsNode(n ast.Node, fset *token.FileSet, srcLines []string,
	tracked map[string]riskyAssign, risks *[]sliceBoundsRisk, enclosingFunc string) {

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
		line:        line,
		varName:     ident.Name,
		index:       indexVal,
		funcName:    ra.funcName,
		fingerprint: enclosingFunc + ":" + ident.Name + "[" + fmt.Sprintf("%d", indexVal) + "]",
	})
}

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

func hasLengthGuard(lines []string, varName string, fromLine, toLine int) bool {
	needle := "len(" + varName + ")"
	const guardWindow = 10
	start := toLine - guardWindow
	if start < fromLine {
		start = fromLine
	}
	for i := start; i <= toLine && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		if strings.Contains(stripCommentsAndStrings1496(lines[i]), needle) {
			return true
		}
	}
	return false
}

func stripCommentsAndStrings1496(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = line[:idx]
	}
	var out strings.Builder
	inString := false
	for _, r := range line {
		switch {
		case r == '"':
			inString = !inString
		case inString:

		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
