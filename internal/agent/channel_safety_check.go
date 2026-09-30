package agent

// Channel Safety: Double-Close and Send-After-Close Detection
//
// Problem: AI coding agents frequently produce Go code with channel close
// misuse that causes runtime panics. The two most dangerous patterns:
//
//  1. Double-close: two close(ch) calls on the same channel in the same
//     function scope. The second close() panics with "close of closed channel".
//
//  2. Send-after-close: a close(ch) followed by ch <- value in the same
//     function scope. The send panics with "send on closed channel".
//
//  3. Close-in-loop: close(ch) inside a for/range loop body, where the
//     channel is created outside the loop. The second iteration's close panics.
//
// These bugs are insidious because:
//   - They are runtime panics, not compile errors
//   - go vet does NOT detect them (no dataflow analysis for channels)
//   - staticcheck does NOT detect them
//   - They manifest non-deterministically (depends on goroutine scheduling)
//   - Tests may pass if timing avoids the problematic path
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on agent judgment)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Devin: no detection
//
// Approach: AST-based analysis within each function scope. Tracks close()
// and send operations per channel variable. Delta-aware: only flags patterns
// newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

// channelSafetyInstance represents a detected channel safety issue.
type channelSafetyInstance struct {
	posStr   string // position of the offending statement
	funcName string // enclosing function (delta key component, #214)
	channel  string // channel variable name
	kind     string // "double-close", "send-after-close", "close-in-loop"
}

// checkChannelSafety performs AST-based channel close safety detection on Go
// source. Returns warnings for newly-introduced channel misuse patterns.
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
		// Key must include the enclosing function and count each instance:
		// a bare channel+kind key made all same-channel instances in the
		// file collapse together, so fixing one function while copying the
		// bad pattern into another was silently suppressed (#214).
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

// formatChannelSafetyWarning converts a channelSafetyInstance into a warning string.
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

// findChannelSafetyIssues parses Go source and returns all channel safety issues.
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
			inst.funcName = fn.Name.Name // delta key component (#214)
			instances = append(instances, inst)
		}
	}

	return instances
}

// chanOp represents a single channel operation (close or send) found in source.
type chanOp struct {
	op       string // "close" or "send"
	name     string
	pos      token.Pos
	deferred bool // true if inside a defer statement
	// #2648: control-flow context so mutually exclusive paths (early
	// return between ops, or ops in sibling branches of the same if)
	// are not flagged as sequential double-close / send-after-close.
	depth int // block nesting depth at the op site
	// #2678: the FULL chain of enclosing if contexts, outermost first.
	// The innermost if alone (#2648) misses else-if chains: the close in
	// `if A {}` and the send in `else if B {}` share the OUTER if as their
	// exclusive ancestor but have different innermost ifs.
	ifStack []ifCtx
	// #2776: enclosing loop positions, outermost first — used to scope
	// break's loop-exit exclusivity to ops inside that same loop.
	loopStack []token.Pos
	// #2938: enclosing switch/select case contexts, outermost first —
	// ops in sibling clauses of the same switch/select never both run
	// (Go case bodies don't fall through; select runs exactly one case).
	caseStack []caseCtx
}

// ifCtx records one enclosing if-statement: its collector-assigned id
// and which side of it the op sits on (1 = then, 2 = else).
type ifCtx struct {
	id   int
	side int
}

// caseCtx records one enclosing switch/select clause: the statement's
// position (unique id within the file) and the clause index (#2938).
type caseCtx struct {
	switchPos token.Pos
	clause    int
}

// terminatorInfo records a flow-terminating statement (return, panic) or a
// loop-exit statement (break) with its block depth — used to detect that ops
// before vs after it cannot both execute (#2648, #2776).
type terminatorInfo struct {
	pos      token.Pos
	depth    int
	loopExit bool      // true for break: terminates only the enclosing loop, not the function
	loopPos  token.Pos // for loopExit: position of the innermost enclosing loop
}

// chanOpCollector walks a function body tracking control-flow context.
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

// inspectExprs finds close()/send ops inside a statement's expressions
// without descending into nested closures (those have their own flow).
func (c *chanOpCollector) inspectExprs(n ast.Node) {
	startDepth := c.depth
	startStack := c.ifStack
	ast.Inspect(n, func(inner ast.Node) bool {
		switch e := inner.(type) {
		case *ast.FuncLit:
			return false // separate flow context; conservative skip
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
		// Reached only when a case body is walked without clause context
		// (defense); clause-aware walks go through walkCaseClauses.
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
		// #2776: break exits the enclosing loop, so ops after it within the
		// loop body (or in iterations that follow) never execute. continue
		// is deliberately NOT recorded: later iterations still run after a
		// continue, so a send after a close+continue is a true positive.
		if s.Tok == token.BREAK {
			var lp token.Pos
			if len(c.loopStack) > 0 {
				lp = c.loopStack[len(c.loopStack)-1]
			}
			c.terminators = append(c.terminators, terminatorInfo{pos: s.Pos(), depth: c.depth, loopExit: true, loopPos: lp})
		}
	case *ast.GoStmt:
		// goroutine body is a separate flow; only its launch is here
	default:
		c.inspectExprs(stmt)
	}
}

// walkCaseClauses walks each case body of a switch/select with its own
// clause context (#2938): only one clause of a switch/select ever runs,
// so ops in sibling clauses are mutually exclusive. A select comm
// (`case ch <- v:`) is itself a send op and is recorded under its clause.
func (c *chanOpCollector) walkCaseClauses(swPos token.Pos, body *ast.BlockStmt) {
	saved := c.caseStack
	for i, cl := range body.List {
		var comm ast.Stmt
		var caseBody []ast.Stmt
		switch cc := cl.(type) {
		case *ast.CaseClause: // switch / type-switch clauses
			caseBody = cc.Body
		case *ast.CommClause: // select clauses; Comm is the case expression
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

// mutuallyExclusive reports whether op b can never execute on a path where
// op a already executed: sibling branches of the same if, or a flow-
// terminating statement between them at a depth no deeper than a's.
func (c *chanOpCollector) mutuallyExclusive(a, b chanOp) bool {
	// Any shared enclosing if taken on opposite sides (#2648 sibling
	// branches, #2678 else-if chains) makes the two ops exclusive.
	for _, ca := range a.ifStack {
		for _, cb := range b.ifStack {
			if ca.id == cb.id && ca.side != cb.side {
				return true
			}
		}
	}
	// #2938: sibling clauses of the same switch/select never both execute
	// (no fallthrough; select runs exactly one case). Only applied when
	// NEITHER op is inside a loop: a loop can re-execute the switch/select
	// across iterations, so loop-carried close/send pairs stay checkable —
	// conservative per #2938 (loop-side risk is detectCloseInLoops' turf).
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
				// break only terminates its innermost enclosing loop: b must
				// also be inside that loop (its loopStack contains it). A send
				// AFTER the loop can still execute and stays checkable.
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

// analyzeChannelOpsInFunc inspects a function body for channel safety issues.
func analyzeChannelOpsInFunc(fset *token.FileSet, body *ast.BlockStmt) []channelSafetyInstance {
	var instances []channelSafetyInstance

	// Collect all close()/send() ops with control-flow context (#2648):
	// a flat ast.Inspect walk loses the early-return / sibling-branch
	// exclusivity that Go semantics guarantee, so error-path close followed
	// by a normal-path close was flagged "will panic".
	var col chanOpCollector
	col.depth = 1 // function body block
	col.walkStmts(body.List)
	ops := col.ops

	// Build per-channel operation sequences.
	chanOps := make(map[string][]chanOp)
	for _, op := range ops {
		chanOps[op.name] = append(chanOps[op.name], op)
	}

	for chName, copList := range chanOps {
		instances = append(instances, detectDoubleClose(fset, chName, copList, &col)...)
		instances = append(instances, detectSendAfterClose(fset, chName, copList, &col)...)
	}

	// Detect close(ch) inside loops where ch is not recreated per iteration.
	instances = append(instances, detectCloseInLoops(fset, body)...)

	return instances
}

// isCloseCall returns true if the call expression is close(ch).
func isCloseCall(ce *ast.CallExpr) bool {
	ident, ok := ce.Fun.(*ast.Ident)
	if !ok || ident.Name != "close" {
		return false
	}
	return len(ce.Args) == 1
}

// channelNameFromArg extracts the channel variable name from a close() argument.
func channelNameFromArg(expr ast.Expr) string {
	return channelNameFromExpr(expr)
}

// channelNameFromExpr extracts the channel identifier name from an expression.
// Handles simple identifiers (ch) and selector expressions (s.ch).
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

// detectDoubleClose flags when close(ch) appears twice for the same channel
// in the same function scope.
func detectDoubleClose(fset *token.FileSet, chName string, ops []chanOp, col *chanOpCollector) []channelSafetyInstance {
	var instances []channelSafetyInstance
	closeCount := 0
	var prevClose chanOp
	havePrev := false
	for _, op := range ops {
		if op.op != "close" {
			continue
		}
		// #2648: a close on a path mutually exclusive with the previous
		// close (early return between them, sibling if branches) can
		// never double-close - restart the count at this close.
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

// detectSendAfterClose flags when a send appears after a close on the same
// channel in the same function scope (by source order).
func detectSendAfterClose(fset *token.FileSet, chName string, ops []chanOp, col *chanOpCollector) []channelSafetyInstance {
	var closeOp chanOp
	haveClose := false
	for _, op := range ops {
		if op.op == "close" {
			// Deferred closes execute at function return - AFTER all sends.
			// They cannot cause send-after-close panics.
			if op.deferred {
				continue
			}
			closeOp = op
			haveClose = true
			continue
		}
		// #2648: a send on a path mutually exclusive with the earlier
		// close (error-branch close + return, sibling branches) can
		// never be send-after-close - skip this send only. The close
		// marker must survive so a later NON-exclusive send is still
		// checked against the same close (it can execute after it).
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

// detectCloseInLoops scans for close(ch) inside loop bodies where ch is not
// recreated per iteration via make(chan...) inside the same loop. Closing a
// channel created outside the loop will panic on the second iteration.
// Exemption (#2699): when the close is followed in the same statement list by
// a statement that terminates the iteration path — a return (not inside a
// func literal) or a bare break that binds to the enclosing loop (no
// select/switch in between) — the close executes at most once and the
// canonical loop-exit idiom must not be flagged. Plain statements (cleanup,
// logging) between the close and the terminator are allowed; any nested
// control-flow construct makes the terminator conditional and voids the
// exemption.
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

// closeLoopWalker walks loop bodies tracking the control-flow context needed
// to decide whether a close(ch) is followed by a guaranteed terminator.
type closeLoopWalker struct {
	fset          *token.FileSet
	createdInLoop map[string]bool
	seen          map[token.Pos]bool
	instances     *[]channelSafetyInstance
}

// scanStmtList walks a statement list. funcLit marks that the list lives
// inside a function literal (return only exits the closure); breakBarrier
// marks that a select/switch sits between this list and the enclosing loop
// (a bare break binds to that select/switch instead of the loop).
func (w *closeLoopWalker) scanStmtList(stmts []ast.Stmt, funcLit, breakBarrier bool) {
	for i, s := range stmts {
		if w.scanCloseBearingStmt(s, closeCtx{stmts: stmts, next: i + 1, funcLit: funcLit, breakBarrier: breakBarrier}) {
			continue
		}
		w.scanContainerStmt(s, funcLit, breakBarrier)
	}
}

// scanCloseBearingStmt handles statement forms whose expressions can carry a
// close call or function literals (expression / defer / go / assign /
// return / send). It reports whether the statement was handled here.
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

// scanContainerStmt descends into nested statement lists (if / for / range /
// select / switch / type-switch / labeled / block). Clause bodies of select
// and switch set the break barrier: a bare break there binds to the clause,
// not the enclosing loop.
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

// scanBody walks a loop body when present.
func (w *closeLoopWalker) scanBody(body *ast.BlockStmt, funcLit, breakBarrier bool) {
	if body != nil {
		w.scanStmtList(body.List, funcLit, breakBarrier)
	}
}

// walkClauseBodies walks the clause bodies of a select/switch statement,
// applying the break barrier (a bare break inside a clause binds to the
// clause, not the enclosing loop).
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

// walkExprForFuncLits finds function literals inside an expression and walks
// their bodies with funcLit set (a return there only exits the closure).
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

// closeCtx carries the control-flow context of a close(ch) call: its
// containing statement list, the index of the first statement after the
// close, and the funcLit/breakBarrier flags describing what a return or bare
// break would actually exit.
type closeCtx struct {
	stmts        []ast.Stmt
	next         int
	funcLit      bool
	breakBarrier bool
}

// recordIfNotExempted records a close(ch) instance unless the channel was
// created in the loop or a guaranteed terminator follows the close in the
// same statement list.
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

// closeFollowedByTerminator reports whether the statements after a close
// guarantee the execution path leaves the enclosing loop before a second
// iteration: a return (outside any func literal) or a bare break that binds
// to the enclosing loop. Plain non-branching statements in between are
// skipped; the first control-flow construct decides, conservatively.
func closeFollowedByTerminator(stmts []ast.Stmt, from int, funcLit, breakBarrier bool) bool {
	for _, s := range stmts[from:] {
		switch n := s.(type) {
		case *ast.ReturnStmt:
			// Inside a func literal a return only exits the closure, so the
			// loop can still reach the close again.
			return !funcLit
		case *ast.BranchStmt:
			// Bare break exits the loop only when no select/switch shadows it;
			// labeled break/goto/continue are not resolved (conservative).
			if n.Tok == token.BREAK && n.Label == nil {
				return !funcLit && !breakBarrier
			}
			return false
		case *ast.ExprStmt, *ast.AssignStmt, *ast.IncDecStmt, *ast.SendStmt,
			*ast.DeclStmt, *ast.EmptyStmt, *ast.DeferStmt, *ast.GoStmt:
			// Plain statements cannot transfer control; keep scanning.
			continue
		default:
			// Any nested control-flow construct (if/for/select/switch/labeled)
			// makes an eventual terminator conditional — no exemption.
			return false
		}
	}
	return false
}

// collectChanMakeNames returns a set of channel variable names created via
// make(chan...) within the given block.
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

// isMakeChanCall returns true if the expression is make(chan ...).
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

// collectChannelSafetyIssues parses old content and returns a set of existing
// channel safety issue signatures for delta-aware suppression.
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
