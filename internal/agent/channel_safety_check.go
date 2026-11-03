package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

type channelSafetyInstance struct {
	posStr   string
	funcName string
	channel  string
	kind     string
}

func checkChannelSafety(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldSet := collectChannelSafetyIssues(oldContent)
	newInstances := findChannelSafetyIssues(newContent)

	var warnings []string
	for _, inst := range newInstances {

		key := inst.funcName + "|" + inst.channel + "|" + inst.kind
		if oldSet[key] > 0 {
			oldSet[key]--
			continue
		}
		msg := formatChannelSafetyWarning(inst)
		if msg != "" {
			warnings = append(warnings, msg)
		}
	}

	if len(warnings) > 3 {
		warnings = warnings[:3]
	}
	return warnings
}

func formatChannelSafetyWarning(inst channelSafetyInstance) string {
	switch inst.kind {
	case "double-close":
		return fmt.Sprintf(
			"Double channel close at %s: channel '%s' is closed more than once in the same function "+
				"scope. The second close() will panic with 'close of closed channel'. "+
				"Ensure each channel is closed exactly once, or guard with sync.Once.",
			inst.posStr, inst.channel)
	case "send-after-close":
		return fmt.Sprintf(
			"Send after close at %s: channel '%s' receives a send (ch <- v) after being closed "+
				"in the same function scope. This will panic with 'send on closed channel'. "+
				"Move the close() call after all sends, or use a done signal instead.",
			inst.posStr, inst.channel)
	case "close-in-loop":
		return fmt.Sprintf(
			"Channel close inside loop at %s: channel '%s' is closed inside a loop body, "+
				"but the channel is not recreated per iteration. The second iteration's close() "+
				"will panic. Move the close() outside the loop or create a new channel per iteration.",
			inst.posStr, inst.channel)
	}
	return ""
}

func findChannelSafetyIssues(src string) []channelSafetyInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []channelSafetyInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, inst := range analyzeChannelOpsInFunc(fset, fn.Body) {
			inst.funcName = fn.Name.Name
			instances = append(instances, inst)
		}
	}

	return instances
}

type chanOp struct {
	op        string
	name      string
	pos       token.Pos
	deferred  bool
	depth     int
	ifStack   []ifCtx
	loopStack []token.Pos
	caseStack []caseCtx
}

type ifCtx struct {
	id   int
	side int
}

type caseCtx struct {
	switchPos token.Pos
	clause    int
}

type terminatorInfo struct {
	pos      token.Pos
	depth    int
	loopExit bool
	loopPos  token.Pos
}

type chanOpCollector struct {
	ops         []chanOp
	terminators []terminatorInfo
	depth       int
	ifStack     []ifCtx
	loopStack   []token.Pos
	caseStack   []caseCtx
	nextIfID    int
}

func (c *chanOpCollector) recordOp(op, name string, pos token.Pos, deferred bool) {
	stack := make([]ifCtx, len(c.ifStack))
	copy(stack, c.ifStack)
	loops := make([]token.Pos, len(c.loopStack))
	copy(loops, c.loopStack)
	cases := make([]caseCtx, len(c.caseStack))
	copy(cases, c.caseStack)
	c.ops = append(c.ops, chanOp{op: op, name: name, pos: pos, deferred: deferred,
		depth: c.depth, ifStack: stack, loopStack: loops, caseStack: cases})
}

func (c *chanOpCollector) inspectExprs(n ast.Node) {
	startDepth := c.depth
	startStack := c.ifStack
	ast.Inspect(n, func(inner ast.Node) bool {
		switch e := inner.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if isCloseCall(e) {
				if chName := channelNameFromArg(e.Args[0]); chName != "" {
					c.recordOp("close", chName, e.Pos(), false)
				}
			}
		case *ast.SendStmt:
			if chName := channelNameFromExpr(e.Chan); chName != "" {
				c.recordOp("send", chName, e.Pos(), false)
			}
		}
		return true
	})
	c.depth, c.ifStack = startDepth, startStack
}

func (c *chanOpCollector) walkStmts(list []ast.Stmt) {
	for _, stmt := range list {
		c.walkStmt(stmt)
	}
}

func (c *chanOpCollector) walkStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		c.depth++
		c.walkStmts(s.List)
		c.depth--
	case *ast.IfStmt:
		c.nextIfID++
		id := c.nextIfID
		savedStack := c.ifStack
		c.ifStack = append(c.ifStack, ifCtx{id: id, side: 1})
		c.walkStmt(s.Body)
		c.ifStack[len(c.ifStack)-1].side = 2
		if s.Else != nil {
			c.walkStmt(s.Else)
		}
		c.ifStack = savedStack
	case *ast.ForStmt:
		c.loopStack = append(c.loopStack, s.Pos())
		c.walkStmt(s.Body)
		c.loopStack = c.loopStack[:len(c.loopStack)-1]
	case *ast.RangeStmt:
		c.loopStack = append(c.loopStack, s.Pos())
		c.walkStmt(s.Body)
		c.loopStack = c.loopStack[:len(c.loopStack)-1]
	case *ast.SwitchStmt:
		c.walkCaseClauses(s.Pos(), s.Body)
	case *ast.TypeSwitchStmt:
		c.walkCaseClauses(s.Pos(), s.Body)
	case *ast.SelectStmt:
		c.walkCaseClauses(s.Pos(), s.Body)
	case *ast.CaseClause:

		c.walkStmts(s.Body)
	case *ast.DeferStmt:
		if ce := s.Call; isCloseCall(ce) {
			if chName := channelNameFromArg(ce.Args[0]); chName != "" {
				c.recordOp("close", chName, ce.Pos(), true)
			}
		}
	case *ast.ReturnStmt:
		c.terminators = append(c.terminators, terminatorInfo{pos: s.Pos(), depth: c.depth})
		c.inspectExprs(s)
	case *ast.BranchStmt:

		if s.Tok == token.BREAK {
			var lp token.Pos
			if len(c.loopStack) > 0 {
				lp = c.loopStack[len(c.loopStack)-1]
			}
			c.terminators = append(c.terminators, terminatorInfo{pos: s.Pos(), depth: c.depth, loopExit: true, loopPos: lp})
		}
	case *ast.GoStmt:

	default:
		c.inspectExprs(stmt)
	}
}

func (c *chanOpCollector) walkCaseClauses(swPos token.Pos, body *ast.BlockStmt) {
	saved := c.caseStack
	for i, cl := range body.List {
		var comm ast.Stmt
		var caseBody []ast.Stmt
		switch cc := cl.(type) {
		case *ast.CaseClause:
			caseBody = cc.Body
		case *ast.CommClause:
			comm = cc.Comm
			caseBody = cc.Body
		default:
			continue
		}
		c.caseStack = append(saved, caseCtx{switchPos: swPos, clause: i})
		if comm != nil {
			if st, ok := comm.(*ast.SendStmt); ok {
				if chName := channelNameFromExpr(st.Chan); chName != "" {
					c.recordOp("send", chName, st.Pos(), false)
				}
			} else {
				c.inspectExprs(comm)
			}
		}
		c.walkStmts(caseBody)
	}
	c.caseStack = saved
}

func (c *chanOpCollector) mutuallyExclusive(a, b chanOp) bool {

	for _, ca := range a.ifStack {
		for _, cb := range b.ifStack {
			if ca.id == cb.id && ca.side != cb.side {
				return true
			}
		}
	}

	if len(a.loopStack) == 0 && len(b.loopStack) == 0 {
		for _, sa := range a.caseStack {
			for _, sb := range b.caseStack {
				if sa.switchPos == sb.switchPos && sa.clause != sb.clause {
					return true
				}
			}
		}
	}
	for _, t := range c.terminators {
		if t.pos > a.pos && t.pos < b.pos && t.depth <= a.depth {
			if t.loopExit {

				if t.loopPos.IsValid() {
					for _, lp := range b.loopStack {
						if lp == t.loopPos {
							return true
						}
					}
				}
				continue
			}
			return true
		}
	}
	return false
}

func analyzeChannelOpsInFunc(fset *token.FileSet, body *ast.BlockStmt) []channelSafetyInstance {
	var instances []channelSafetyInstance

	var col chanOpCollector
	col.depth = 1
	col.walkStmts(body.List)
	ops := col.ops

	chanOps := make(map[string][]chanOp)
	for _, op := range ops {
		chanOps[op.name] = append(chanOps[op.name], op)
	}

	for chName, copList := range chanOps {
		instances = append(instances, detectDoubleClose(fset, chName, copList, &col)...)
		instances = append(instances, detectSendAfterClose(fset, chName, copList, &col)...)
	}

	instances = append(instances, detectCloseInLoops(fset, body)...)

	return instances
}

func isCloseCall(ce *ast.CallExpr) bool {
	ident, ok := ce.Fun.(*ast.Ident)
	if !ok || ident.Name != "close" {
		return false
	}
	return len(ce.Args) == 1
}

func channelNameFromArg(expr ast.Expr) string {
	return channelNameFromExpr(expr)
}

func channelNameFromExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		base := channelNameFromExpr(e.X)
		if base == "" {
			return ""
		}
		return base + "." + e.Sel.Name
	default:
		return ""
	}
}

func detectDoubleClose(fset *token.FileSet, chName string, ops []chanOp, col *chanOpCollector) []channelSafetyInstance {
	var instances []channelSafetyInstance
	closeCount := 0
	var prevClose chanOp
	havePrev := false
	for _, op := range ops {
		if op.op != "close" {
			continue
		}

		if havePrev && col.mutuallyExclusive(prevClose, op) {
			closeCount = 0
		}
		prevClose = op
		havePrev = true
		closeCount++
		if closeCount >= 2 {
			instances = append(instances, channelSafetyInstance{
				posStr:  fset.Position(op.pos).String(),
				channel: chName,
				kind:    "double-close",
			})
		}
	}
	return instances
}

func detectSendAfterClose(fset *token.FileSet, chName string, ops []chanOp, col *chanOpCollector) []channelSafetyInstance {
	var closeOp chanOp
	haveClose := false
	for _, op := range ops {
		if op.op == "close" {

			if op.deferred {
				continue
			}
			closeOp = op
			haveClose = true
			continue
		}

		if haveClose && op.op == "send" {
			if col.mutuallyExclusive(closeOp, op) {
				continue
			}
			return []channelSafetyInstance{{
				posStr:  fset.Position(op.pos).String(),
				channel: chName,
				kind:    "send-after-close",
			}}
		}
	}
	return nil
}

func detectCloseInLoops(fset *token.FileSet, body *ast.BlockStmt) []channelSafetyInstance {
	var instances []channelSafetyInstance
	seen := make(map[token.Pos]bool)

	ast.Inspect(body, func(node ast.Node) bool {
		var loopBody *ast.BlockStmt
		switch n := node.(type) {
		case *ast.ForStmt:
			loopBody = n.Body
		case *ast.RangeStmt:
			loopBody = n.Body
		default:
			return true
		}
		if loopBody == nil {
			return true
		}

		createdInLoop := collectChanMakeNames(loopBody)

		w := &closeLoopWalker{
			fset:          fset,
			createdInLoop: createdInLoop,
			seen:          seen,
			instances:     &instances,
		}
		w.scanStmtList(loopBody.List, false, false)
		return true
	})

	return instances
}

type closeLoopWalker struct {
	fset          *token.FileSet
	createdInLoop map[string]bool
	seen          map[token.Pos]bool
	instances     *[]channelSafetyInstance
}

func (w *closeLoopWalker) scanStmtList(stmts []ast.Stmt, funcLit, breakBarrier bool) {
	for i, s := range stmts {
		if w.scanCloseBearingStmt(s, closeCtx{stmts: stmts, next: i + 1, funcLit: funcLit, breakBarrier: breakBarrier}) {
			continue
		}
		w.scanContainerStmt(s, funcLit, breakBarrier)
	}
}

func (w *closeLoopWalker) scanCloseBearingStmt(s ast.Stmt, ctx closeCtx) bool {
	switch n := s.(type) {
	case *ast.ExprStmt:
		if ce, ok := n.X.(*ast.CallExpr); ok && isCloseCall(ce) {
			w.recordIfNotExempted(ce, ctx)
		} else {
			w.walkExprForFuncLits(n.X, ctx.breakBarrier)
		}
	case *ast.DeferStmt:
		if isCloseCall(n.Call) {
			w.recordIfNotExempted(n.Call, ctx)
		} else {
			w.walkExprForFuncLits(n.Call, ctx.breakBarrier)
		}
	case *ast.GoStmt:
		if isCloseCall(n.Call) {
			w.recordIfNotExempted(n.Call, ctx)
		} else {
			w.walkExprForFuncLits(n.Call, ctx.breakBarrier)
		}
	case *ast.AssignStmt:
		for _, rhs := range n.Rhs {
			w.walkExprForFuncLits(rhs, ctx.breakBarrier)
		}
	case *ast.ReturnStmt:
		for _, v := range n.Results {
			w.walkExprForFuncLits(v, ctx.breakBarrier)
		}
	case *ast.SendStmt:
		w.walkExprForFuncLits(n.Value, ctx.breakBarrier)
	default:
		return false
	}
	return true
}

func (w *closeLoopWalker) scanContainerStmt(s ast.Stmt, funcLit, breakBarrier bool) {
	switch n := s.(type) {
	case *ast.IfStmt:
		w.walkExprForFuncLits(n.Cond, breakBarrier)
		w.scanStmtList(n.Body.List, funcLit, breakBarrier)
		if elseBlock, ok := n.Else.(*ast.BlockStmt); ok {
			w.scanStmtList(elseBlock.List, funcLit, breakBarrier)
		} else if elseIf, ok := n.Else.(*ast.IfStmt); ok {
			w.scanStmtList([]ast.Stmt{elseIf}, funcLit, breakBarrier)
		}
	case *ast.ForStmt:
		w.scanBody(n.Body, funcLit, breakBarrier)
	case *ast.RangeStmt:
		w.scanBody(n.Body, funcLit, breakBarrier)
	case *ast.SelectStmt:
		w.walkClauseBodies(n.Body, funcLit)
	case *ast.SwitchStmt:
		w.walkClauseBodies(n.Body, funcLit)
	case *ast.TypeSwitchStmt:
		w.walkClauseBodies(n.Body, funcLit)
	case *ast.LabeledStmt:
		w.scanStmtList([]ast.Stmt{n.Stmt}, funcLit, breakBarrier)
	case *ast.BlockStmt:
		w.scanStmtList(n.List, funcLit, breakBarrier)
	}
}

func (w *closeLoopWalker) scanBody(body *ast.BlockStmt, funcLit, breakBarrier bool) {
	if body != nil {
		w.scanStmtList(body.List, funcLit, breakBarrier)
	}
}

func (w *closeLoopWalker) walkClauseBodies(body *ast.BlockStmt, funcLit bool) {
	if body == nil {
		return
	}
	for _, cc := range body.List {
		switch c := cc.(type) {
		case *ast.CommClause:
			w.scanStmtList(c.Body, funcLit, true)
		case *ast.CaseClause:
			w.scanStmtList(c.Body, funcLit, true)
		}
	}
}

func (w *closeLoopWalker) walkExprForFuncLits(expr ast.Expr, breakBarrier bool) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if fl, ok := node.(*ast.FuncLit); ok {
			w.scanStmtList(fl.Body.List, true, breakBarrier)
			return false
		}
		return true
	})
}

type closeCtx struct {
	stmts        []ast.Stmt
	next         int
	funcLit      bool
	breakBarrier bool
}

func (w *closeLoopWalker) recordIfNotExempted(ce *ast.CallExpr, ctx closeCtx) {
	if w.seen[ce.Pos()] {
		return
	}
	chName := channelNameFromArg(ce.Args[0])
	if chName == "" || w.createdInLoop[chName] {
		return
	}
	w.seen[ce.Pos()] = true
	if closeFollowedByTerminator(ctx.stmts, ctx.next, ctx.funcLit, ctx.breakBarrier) {
		return
	}
	*w.instances = append(*w.instances, channelSafetyInstance{
		posStr:  w.fset.Position(ce.Pos()).String(),
		channel: chName,
		kind:    "close-in-loop",
	})
}

func closeFollowedByTerminator(stmts []ast.Stmt, from int, funcLit, breakBarrier bool) bool {
	for _, s := range stmts[from:] {
		switch n := s.(type) {
		case *ast.ReturnStmt:

			return !funcLit
		case *ast.BranchStmt:

			if n.Tok == token.BREAK && n.Label == nil {
				return !funcLit && !breakBarrier
			}
			return false
		case *ast.ExprStmt, *ast.AssignStmt, *ast.IncDecStmt, *ast.SendStmt,
			*ast.DeclStmt, *ast.EmptyStmt, *ast.DeferStmt, *ast.GoStmt:

			continue
		default:

			return false
		}
	}
	return false
}

func collectChanMakeNames(block *ast.BlockStmt) map[string]bool {
	result := make(map[string]bool)
	ast.Inspect(block, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Rhs) == 0 || len(as.Lhs) == 0 {
			return true
		}
		for i, rhs := range as.Rhs {
			if !isMakeChanCall(rhs) || i >= len(as.Lhs) {
				continue
			}
			if ident, ok := as.Lhs[i].(*ast.Ident); ok {
				result[ident.Name] = true
			}
		}
		return true
	})
	return result
}

func isMakeChanCall(expr ast.Expr) bool {
	ce, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := ce.Fun.(*ast.Ident)
	if !ok || ident.Name != "make" {
		return false
	}
	if len(ce.Args) == 0 {
		return false
	}
	_, isChan := ce.Args[0].(*ast.ChanType)
	return isChan
}

func collectChannelSafetyIssues(src string) map[string]int {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	instances := findChannelSafetyIssues(src)
	result := make(map[string]int, len(instances))
	for _, inst := range instances {
		result[inst.funcName+"|"+inst.channel+"|"+inst.kind]++
	}
	return result
}
