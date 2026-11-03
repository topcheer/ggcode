package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	goModVersionMu    sync.Mutex
	goMod122PlusCache = make(map[string]bool)
)

func go122PlusFor(filePath string) bool {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	dir := filepath.Dir(abs)

	goModVersionMu.Lock()
	if v, ok := goMod122PlusCache[dir]; ok {
		goModVersionMu.Unlock()
		return v
	}
	goModVersionMu.Unlock()

	v := detectGo122Plus(dir)

	goModVersionMu.Lock()
	goMod122PlusCache[dir] = v
	goModVersionMu.Unlock()
	return v
}

func detectGo122Plus(dir string) bool {
	for {
		if content, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			return goModDeclares122Plus(content)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func goModDeclares122Plus(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		parts := strings.Fields(strings.TrimPrefix(line, "go "))
		if len(parts) == 0 {
			return false
		}
		nums := strings.Split(parts[0], ".")
		major, minor := nums[0], ""
		if len(nums) > 1 {
			minor = nums[1]
		}
		if major != "1" {
			return major >= "2"
		}
		return len(minor) >= 2 && minor >= "22"
	}
	return false
}

type loopCaptureInstance struct {
	posStr   string
	varName  string
	loopType string
	kind     string
	funcName string
}

func checkLoopVarCapture(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	is122 := go122PlusFor(filePath)

	oldSet := collectLoopCaptureIssues(oldContent)
	newInstances := findLoopCaptureIssues(newContent)

	var warnings []string
	for _, inst := range newInstances {

		key := inst.funcName + "|" + inst.varName + "|" + inst.kind + "|" + inst.loopType
		if oldSet[key] {
			continue
		}
		warnings = append(warnings, formatLoopCaptureWarning(inst, is122))
	}

	if len(warnings) > 3 {
		warnings = warnings[:3]
	}
	return warnings
}

func formatLoopCaptureWarning(inst loopCaptureInstance, is122 bool) string {
	fixHint := " Pass it as a parameter: go func(item T) { use(item) }(item), or rebind: item := item before the closure."
	if inst.kind == "goroutine" {

		rangeWarning := "all goroutines may see the last iteration's value (classic Go gotcha)."
		if is122 {
			rangeWarning = "may still be a bug if the closure is delayed or saved for later use."
		}
		return fmt.Sprintf(
			"Loop variable '%s' captured in goroutine at %s inside %s loop: "+
				"%s%s",
			inst.varName, inst.posStr, inst.loopType, rangeWarning, fixHint)
	}

	deferWarning := "the deferred call will use the variable's final value, not the value at defer time."
	if is122 {
		deferWarning = "Go 1.22+ captures a per-iteration variable here, so this is usually safe; " +
			"defers still accumulate until function exit - watch memory and execution order."
	}
	deferHint := " Evaluate now if you need defer-time semantics: defer func() { use(item) }(item)."
	return fmt.Sprintf(
		"Loop variable '%s' captured in deferred closure at %s inside %s loop: "+
			"%s%s",
		inst.varName, inst.posStr, inst.loopType, deferWarning, deferHint)
}

func findLoopCaptureIssues(src string) []loopCaptureInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []loopCaptureInstance
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcName := fn.Name.Name
		instances = append(instances, analyzeLoopsForCapture(fset, funcName, fn.Body)...)
	}
	return instances
}

func analyzeLoopsForCapture(fset *token.FileSet, funcName string, body *ast.BlockStmt) []loopCaptureInstance {
	var instances []loopCaptureInstance

	ast.Inspect(body, func(node ast.Node) bool {
		var loopVars []string
		var loopBody *ast.BlockStmt
		var loopType string

		switch n := node.(type) {
		case *ast.RangeStmt:
			loopVars = rangeLoopVars(n)
			loopBody = n.Body
			loopType = "range"
		case *ast.ForStmt:
			loopVars = forLoopVars(n)
			loopBody = n.Body
			loopType = "for"
		default:
			return true
		}

		if len(loopVars) == 0 || loopBody == nil {
			return true
		}

		reboundAt := collectReboundVars(loopBody, loopVars)
		for _, v := range loopVars {
			instances = append(instances, findCaptureInBody(fset, funcName, loopBody, v, loopType, reboundAt)...)
		}
		return true
	})

	return instances
}

func rangeLoopVars(rs *ast.RangeStmt) []string {
	var vars []string
	if id, ok := rs.Key.(*ast.Ident); ok && id.Name != "_" {
		vars = append(vars, id.Name)
	}
	if id, ok := rs.Value.(*ast.Ident); ok && id.Name != "_" {
		vars = append(vars, id.Name)
	}
	return vars
}

func forLoopVars(fs *ast.ForStmt) []string {
	if fs.Init == nil {
		return nil
	}
	as, ok := fs.Init.(*ast.AssignStmt)
	if !ok {
		return nil
	}
	var vars []string
	for _, lhs := range as.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
			vars = append(vars, id.Name)
		}
	}
	return vars
}

func collectReboundVars(body *ast.BlockStmt, loopVars []string) map[string]token.Pos {
	target := make(map[string]bool)
	for _, v := range loopVars {
		target[v] = true
	}
	result := make(map[string]token.Pos)
	record := func(name string, pos token.Pos) {
		if prev, ok := result[name]; !ok || pos < prev {
			result[name] = pos
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {

			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			return true
		}
		scanRebindAssignment(as, target, record)
		return true
	})
	return result
}

func scanRebindAssignment(as *ast.AssignStmt, target map[string]bool, record func(name string, pos token.Pos)) {
	for i, lhs := range as.Lhs {
		if i >= len(as.Rhs) {
			return
		}
		lhsID, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		rhsID, ok := as.Rhs[i].(*ast.Ident)
		if !ok {
			continue
		}
		if lhsID.Name == rhsID.Name && target[lhsID.Name] {
			record(lhsID.Name, lhsID.Pos())
		}
	}
}

func findCaptureInBody(fset *token.FileSet, funcName string, body *ast.BlockStmt, varName, loopType string, reboundAt map[string]token.Pos) []loopCaptureInstance {
	var instances []loopCaptureInstance

	ast.Inspect(body, func(node ast.Node) bool {
		fl, kind := extractCaptureTarget(node)
		if fl == nil {
			return true
		}

		if collectFuncLitParams(fl)[varName] {
			return true
		}

		if p, ok := reboundAt[varName]; ok && fl.Pos() > p {
			return true
		}

		if closureCapturesVar(fl.Body, varName) {
			instances = append(instances, loopCaptureInstance{
				posStr:   fset.Position(fl.Pos()).String(),
				varName:  varName,
				loopType: loopType,
				kind:     kind,
				funcName: funcName,
			})
		}
		return true
	})

	return instances
}

func extractCaptureTarget(node ast.Node) (*ast.FuncLit, string) {
	switch n := node.(type) {
	case *ast.GoStmt:
		if fl, ok := n.Call.Fun.(*ast.FuncLit); ok {
			return fl, "goroutine"
		}
	case *ast.DeferStmt:
		if fl, ok := n.Call.Fun.(*ast.FuncLit); ok {
			return fl, "defer"
		}
	}
	return nil, ""
}

func collectFuncLitParams(fl *ast.FuncLit) map[string]bool {
	result := make(map[string]bool)
	if fl.Type == nil || fl.Type.Params == nil {
		return result
	}
	for _, field := range fl.Type.Params.List {
		for _, name := range field.Names {
			result[name.Name] = true
		}
	}
	return result
}

func closureCapturesVar(flBody ast.Node, name string) bool {
	declPos := firstShadowDeclPos(flBody, name)
	captured := false
	ast.Inspect(flBody, func(n ast.Node) bool {
		if captured {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			if declPos == token.NoPos || id.Pos() < declPos {
				captured = true
			}
		}
		return !captured
	})
	return captured
}

func firstShadowDeclPos(body ast.Node, name string) token.Pos {
	declPos := token.NoPos
	register := func(pos token.Pos) {
		if declPos == token.NoPos || pos < declPos {
			declPos = pos
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		var declared []*ast.Ident
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						declared = append(declared, id)
					}
				}
			}
		case *ast.ValueSpec:
			declared = node.Names
		case *ast.RangeStmt:
			for _, expr := range []ast.Expr{node.Key, node.Value} {
				if id, ok := expr.(*ast.Ident); ok {
					declared = append(declared, id)
				}
			}
		case *ast.FuncType:
			if node.Params != nil {
				for _, field := range node.Params.List {
					declared = append(declared, field.Names...)
				}
			}
		}
		for _, id := range declared {
			if id.Name == name {
				register(id.Pos())
			}
		}
		return true
	})
	return declPos
}

func collectLoopCaptureIssues(src string) map[string]bool {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	instances := findLoopCaptureIssues(src)
	result := make(map[string]bool, len(instances))
	for _, inst := range instances {

		key := inst.funcName + "|" + inst.varName + "|" + inst.kind + "|" + inst.loopType
		result[key] = true
	}
	return result
}
