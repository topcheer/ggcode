package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type nilDerefInstance struct {
	posStr    string
	key       string
	suffixKey string
	varName   string
}

type nilDerefCtx struct {
	fset   *token.FileSet
	fnName string
}

type nilDerefDeltaIndex struct {
	instances []oldNilDerefToken
}

type oldNilDerefToken struct {
	key       string
	suffixKey string
	used      bool
}

func (idx *nilDerefDeltaIndex) matchOldNilDeref(inst nilDerefInstance) bool {
	for i := range idx.instances {
		t := &idx.instances[i]
		if !t.used && t.key == inst.key {
			t.used = true
			return true
		}
	}
	for i := range idx.instances {
		t := &idx.instances[i]
		if !t.used && t.suffixKey == inst.suffixKey {
			t.used = true
			return true
		}
	}
	return false
}

func collectOldNilDerefIndex(filePath, oldContent string) nilDerefDeltaIndex {
	idx := nilDerefDeltaIndex{}
	if strings.TrimSpace(oldContent) == "" {
		return idx
	}
	oldFset := token.NewFileSet()
	oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
	if oldErr != nil {
		return idx
	}
	for _, decl := range oldFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, inst := range findNilDerefsInFunc(oldFset, fn) {
			idx.instances = append(idx.instances, oldNilDerefToken{key: inst.key, suffixKey: inst.suffixKey})
		}
	}
	return idx
}

func formatNilDerefReport(instances []nilDerefInstance) string {
	var b strings.Builder
	b.WriteString("[nil-deref-after-error] Pointers dereferenced before error check - may panic:\n")
	for _, inst := range instances {
		b.WriteString(fmt.Sprintf("  - %s: '%s' is dereferenced before an 'if err != nil' check. "+
			"When the error is non-nil, functions often return nil for the primary value. "+
			"Add `if err != nil { return err }` before using '%s'.\n",
			inst.posStr, inst.varName, inst.varName))
	}
	return b.String()
}

func checkNilDerefAfterError(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	if isTestFile(filePath) {
		return ""
	}

	file, fset, err := parseGoSource(filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	var instances []nilDerefInstance
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		instances = append(instances, findNilDerefsInFunc(fset, fn)...)
	}

	oldIdx := collectOldNilDerefIndex(filePath, oldContent)
	var newInstances []nilDerefInstance
	for _, inst := range instances {
		if oldIdx.matchOldNilDeref(inst) {
			continue
		}
		newInstances = append(newInstances, inst)
	}

	if len(newInstances) == 0 {
		return ""
	}
	return formatNilDerefReport(newInstances)
}

type nilRiskEntry struct {
	pos     int
	errName string
	cleared bool
}

type nilDerefWalker struct {
	ctx       *nilDerefCtx
	nilRisk   map[string]nilRiskEntry
	instances []nilDerefInstance
}

func findNilDerefsInFunc(fset *token.FileSet, fn *ast.FuncDecl) []nilDerefInstance {
	fnName := "<anonymous>"
	if fn.Name != nil {
		fnName = fn.Name.Name
	}
	w := &nilDerefWalker{
		ctx:     &nilDerefCtx{fset: fset, fnName: fnName},
		nilRisk: make(map[string]nilRiskEntry),
	}
	w.walk(fn.Body)
	return w.instances
}

func (w *nilDerefWalker) evalVisit(n ast.Node) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(node ast.Node) bool {
		if lit, ok := node.(*ast.FuncLit); ok {
			w.walk(lit.Body)
			return false
		}
		if inst := detectNilDeref(w.ctx, node, w.nilRisk); inst != nil {
			w.instances = append(w.instances, inst...)
		}
		return true
	})
}

func (w *nilDerefWalker) walkExpressionStatements(st ast.Node) bool {
	switch s := st.(type) {
	case *ast.AssignStmt:
		processAssignment(s, w.nilRisk)

		for _, lhs := range s.Lhs {
			w.evalVisit(lhs)
		}
		for _, rhs := range s.Rhs {
			w.evalVisit(rhs)
		}
	case *ast.ExprStmt:
		w.evalVisit(s.X)
	case *ast.SendStmt:
		w.evalVisit(s.Chan)
		w.evalVisit(s.Value)
	case *ast.IncDecStmt:
		w.evalVisit(s.X)
	case *ast.GoStmt:
		w.evalVisit(s.Call)
	case *ast.DeferStmt:
		w.evalVisit(s.Call)
	case *ast.ReturnStmt:
		for _, res := range s.Results {
			w.evalVisit(res)
		}
	case *ast.DeclStmt:
		w.evalVisit(s.Decl)
	default:
		return false
	}
	return true
}

func (w *nilDerefWalker) walk(n ast.Node) {
	if n == nil {
		return
	}

	if blk, ok := n.(*ast.BlockStmt); ok {
		w.walkBlock(blk)
		return
	}

	if is, ok := n.(*ast.IfStmt); ok {
		walkErrorCheckIf(is, w.nilRisk, w.walk)
		return
	}

	if w.walkExpressionStatements(n) {
		return
	}

	w.walkCompound(n)
}

func (w *nilDerefWalker) walkCompound(n ast.Node) {
	switch s := n.(type) {
	case *ast.LabeledStmt:
		w.walk(s.Stmt)
	case *ast.ForStmt:
		w.walk(s.Init)
		w.evalVisit(s.Cond)
		w.walk(s.Post)
		w.walk(s.Body)
	case *ast.RangeStmt:
		w.evalVisit(s.Key)
		w.evalVisit(s.Value)
		w.evalVisit(s.X)
		w.walk(s.Body)
	case *ast.SwitchStmt:
		w.walk(s.Init)
		w.evalVisit(s.Tag)
		w.walk(s.Body)
	case *ast.TypeSwitchStmt:
		w.walk(s.Init)
		w.walk(s.Assign)
		w.walk(s.Body)
	case *ast.SelectStmt:
		w.walk(s.Body)
	case *ast.CaseClause:
		for _, e := range s.List {
			w.evalVisit(e)
		}
		for _, stmt := range s.Body {
			w.walk(stmt)
		}
	case *ast.CommClause:
		w.walk(s.Comm)
		for _, stmt := range s.Body {
			w.walk(stmt)
		}
	default:
		w.evalVisit(n)
	}
}

func (w *nilDerefWalker) walkBlock(blk *ast.BlockStmt) {
	existed := make(map[string]bool, len(w.nilRisk))
	for name := range w.nilRisk {
		existed[name] = true
	}
	for _, st := range blk.List {
		w.walk(st)
	}
	for name := range w.nilRisk {
		if !existed[name] {
			delete(w.nilRisk, name)
		}
	}
}

func derefPathText(e ast.Expr) string {
	parts := make([]string, 0, 4)
	cur := e
	for depth := 0; cur != nil && depth < 64; depth++ {
		switch t := cur.(type) {
		case *ast.SelectorExpr:
			parts = append(parts, "."+t.Sel.Name)
			cur = t.X
		case *ast.IndexExpr:
			parts = append(parts, "[..]")
			cur = t.X
		case *ast.StarExpr:
			parts = append(parts, "*")
			cur = t.X
		case *ast.ParenExpr:
			cur = t.X
		case *ast.Ident:
			parts = append(parts, t.Name)
			cur = nil
		default:
			parts = append(parts, "?")
			cur = nil
		}
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

func nilDerefKey(ctx *nilDerefCtx, node ast.Expr, varName string) string {
	return ctx.fnName + "|" + derefPathText(node) + "|" + varName
}

func nilDerefSuffixKey(node ast.Expr, varName string) string {
	return derefPathText(node) + "|" + varName
}

func processAssignment(assign *ast.AssignStmt, nilRisk map[string]nilRiskEntry) {

	if len(assign.Lhs) < 2 {
		clearReassignedRisk(assign, nilRisk)
		return
	}

	lastLHS := assign.Lhs[len(assign.Lhs)-1]
	errName, ok := lastLHS.(*ast.Ident)
	if !ok || !looksLikeError(errName.Name) {
		return
	}

	if isCommaOkRhs(assign) {
		return
	}

	for i := 0; i < len(assign.Lhs)-1; i++ {
		ident, ok := assign.Lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}

		nilRisk[ident.Name] = nilRiskEntry{
			pos:     int(assign.Pos()),
			errName: errName.Name,
		}
	}
}

func isCommaOkRhs(assign *ast.AssignStmt) bool {
	if len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return false
	}
	switch e := ast.Unparen(assign.Rhs[0]).(type) {
	case *ast.IndexExpr, *ast.TypeAssertExpr:
		return true
	case *ast.UnaryExpr:
		return e.Op == token.ARROW
	}
	return false
}

func clearReassignedRisk(assign *ast.AssignStmt, nilRisk map[string]nilRiskEntry) {
	if len(assign.Lhs) != 1 {
		return
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" {
		return
	}
	if e, risky := nilRisk[ident.Name]; !risky || e.cleared {

		return
	}
	for _, rhs := range assign.Rhs {
		if isNonNullAssignExpr(rhs) {
			delete(nilRisk, ident.Name)
		}
	}
}

func isNonNullAssignExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.UnaryExpr:
		return v.Op == token.AND
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok {
			return id.Name == "new"
		}
	}
	return false
}

func walkErrorCheckIf(is *ast.IfStmt, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {

	if is.Init != nil {
		walk(is.Init)
	}

	bin, ok := is.Cond.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		if ok && bin.Op == token.LAND {

			walkLandCondIf(is, nilRisk, walk)
			return
		}

		walk(is.Cond)
		walk(is.Body)
		walk(is.Else)
		return
	}

	if v := valueNilCheckedVar(bin, nilRisk); v != "" {
		walkValueNilCheckIf(is, bin.Op, v, nilRisk, walk)
		return
	}

	if !isErrorNilCheck(bin) {
		walk(is.Body)
		walk(is.Else)
		return
	}

	errName := ""
	if ident, ok := bin.X.(*ast.Ident); ok && isErrIdent(ident) {
		errName = ident.Name
	} else if ident, ok := bin.Y.(*ast.Ident); ok && isErrIdent(ident) {
		errName = ident.Name
	}
	applyErrNilScopeSemantics(is, bin.Op, errName, nilRisk, walk)
}

func splitLandConjuncts(e ast.Expr) []ast.Expr {
	var out []ast.Expr
	var rec func(ast.Expr)
	rec = func(x ast.Expr) {
		switch t := x.(type) {
		case *ast.BinaryExpr:
			if t.Op == token.LAND {
				rec(t.X)
				rec(t.Y)
				return
			}
			out = append(out, t)
		case *ast.ParenExpr:
			rec(t.X)
		default:
			out = append(out, x)
		}
	}
	rec(e)
	return out
}

func walkLandCondIf(is *ast.IfStmt, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	var restore []func()
	suppress := func(name string) {
		if _, risky := nilRisk[name]; !risky {
			return
		}
		setVarCleared(nilRisk, name, true)
		restore = append(restore, func() { setVarCleared(nilRisk, name, false) })
	}
	for _, conjunct := range splitLandConjuncts(is.Cond) {
		walk(conjunct)
		bin, ok := ast.Unparen(conjunct).(*ast.BinaryExpr)
		if !ok {
			continue
		}
		switch bin.Op {
		case token.NEQ:

			if v := valueNilCheckedVar(bin, nilRisk); v != "" {
				suppress(v)
			}
		case token.EQL:

			errName := ""
			if x, ok := bin.X.(*ast.Ident); ok && isErrIdent(x) && isNilIdent(bin.Y) {
				errName = x.Name
			} else if y, ok := bin.Y.(*ast.Ident); ok && isErrIdent(y) && isNilIdent(bin.X) {
				errName = y.Name
			}
			if errName == "" {
				break
			}
			for k, e := range nilRisk {
				if e.errName == errName {
					suppress(k)
				}
			}
		}
	}
	walk(is.Body)
	for i := len(restore) - 1; i >= 0; i-- {
		restore[i]()
	}
	walk(is.Else)
}

func applyErrNilScopeSemantics(is *ast.IfStmt, op token.Token, errName string, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {

	saved := make(map[string]nilRiskEntry)
	for k, e := range nilRisk {
		if e.errName == errName {
			saved[k] = e
		}
	}
	suppressSaved := func() { setSavedCleared(nilRisk, saved, true) }
	unsuppressSaved := func() { setSavedCleared(nilRisk, saved, false) }
	permanentlyClear := func() {
		for k := range saved {
			delete(nilRisk, k)
		}
	}

	switch op {
	case token.EQL:
		suppressSaved()
		walk(is.Body)
		unsuppressSaved()
		if is.Else != nil {
			walk(is.Else)
		}
	case token.NEQ:
		walk(is.Body)
		thenTerminates := ifBodyTerminates(is.Body)
		if thenTerminates {
			permanentlyClear()
		}
		if is.Else != nil {
			suppressSaved()
			walk(is.Else)
			if !thenTerminates {

				unsuppressSaved()
			}
		}
	}
}

func setVarCleared(nilRisk map[string]nilRiskEntry, name string, cleared bool) {
	if e, ok := nilRisk[name]; ok {
		e.cleared = cleared
		nilRisk[name] = e
	}
}

func setSavedCleared(nilRisk, saved map[string]nilRiskEntry, cleared bool) {
	for k := range saved {
		setVarCleared(nilRisk, k, cleared)
	}
}

func valueNilCheckedVar(bin *ast.BinaryExpr, nilRisk map[string]nilRiskEntry) string {
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return ""
	}
	if x, ok := bin.X.(*ast.Ident); ok && isNilIdent(bin.Y) && !isErrIdent(x) {
		if _, risky := nilRisk[x.Name]; risky {
			return x.Name
		}
	}
	if y, ok := bin.Y.(*ast.Ident); ok && isNilIdent(bin.X) && !isErrIdent(y) {
		if _, risky := nilRisk[y.Name]; risky {
			return y.Name
		}
	}
	return ""
}

func walkValueNilCheckIf(is *ast.IfStmt, op token.Token, varName string, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	suppressVar := func() {
		if e, ok := nilRisk[varName]; ok {
			e.cleared = true
			nilRisk[varName] = e
		}
	}
	unsuppressVar := func() {
		if e, ok := nilRisk[varName]; ok {
			e.cleared = false
			nilRisk[varName] = e
		}
	}

	if op == token.NEQ {
		suppressVar()
		walk(is.Body)
		unsuppressVar()
		if is.Else != nil {
			walk(is.Else)
		}
		return
	}

	walk(is.Body)
	if ifBodyTerminates(is.Body) {
		delete(nilRisk, varName)
	}
	if is.Else != nil {
		suppressVar()
		walk(is.Else)
		unsuppressVar()
	}
}

var noReturnSelectorCalls = map[string]bool{
	"log.Fatal":      true,
	"log.Fatalf":     true,
	"log.Fatalln":    true,
	"os.Exit":        true,
	"runtime.Goexit": true,
	"t.Fatal":        true,
	"t.Fatalf":       true,
	"t.Fatalln":      true,
}

func ifBodyTerminates(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	last := body.List[len(body.List)-1]
	switch st := last.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		if call, ok := st.X.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				return true
			}

			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					if noReturnSelectorCalls[pkg.Name+"."+sel.Sel.Name] {
						return true
					}
				}
			}
		}
	}
	return false
}

func isErrorNilCheck(bin *ast.BinaryExpr) bool {
	leftErr := isErrIdent(bin.X) && isNilIdent(bin.Y)
	rightErr := isErrIdent(bin.Y) && isNilIdent(bin.X)
	return leftErr || rightErr
}

func isErrIdent(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return looksLikeError(ident.Name)
}

func detectNilDeref(ctx *nilDerefCtx, n ast.Node, nilRisk map[string]nilRiskEntry) []nilDerefInstance {
	fset := ctx.fset
	var instances []nilDerefInstance

	switch node := n.(type) {
	case *ast.SelectorExpr:

		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{

					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name)
			}
		}

	case *ast.IndexExpr:

		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{

					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name)
			}
		}

	case *ast.StarExpr:

		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{

					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name)
			}
		}
	}

	return instances
}

func looksLikeError(name string) bool {
	switch name {
	case "err", "e", "errs", "retErr", "callErr":
		return true
	default:

		if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
			return true
		}

		if len(name) > 3 && name[:3] == "err" {
			rest := name[3:]
			isDigits := rest != ""
			for _, c := range rest {
				if c < '0' || c > '9' {
					isDigits = false
					break
				}
			}
			if isDigits {
				return true
			}
		}
		return false
	}
}

func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
