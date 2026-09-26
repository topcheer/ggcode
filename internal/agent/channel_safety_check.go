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
	"go/parser"
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

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
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
	// exitsAfter: the operation's statement is followed (in the same block)
	// by a terminating statement (return/break/continue/goto/panic), so later
	// operations in this function cannot execute after it (#2776: close+return
	// producer-exit and mutex error-handling are safe Go idioms).
	exitsAfter bool
	// branchFrames records which side of each enclosing if/else the op is on.
	// Two ops on different sides of the same if/else are mutually exclusive.
	branchFrames []branchFrame
}

// branchFrame records one if/else branch decision point.
type branchFrame struct {
	ifPos token.Pos // position of the enclosing IfStmt
	side  int       // 0 = then branch, 1 = else branch
}

// mutuallyExclusiveOps returns true if two ops can never both execute
// because they sit on different sides of the same if/else (#2776).
func mutuallyExclusiveOps(a, b chanOp) bool {
	for _, fa := range a.branchFrames {
		for _, fb := range b.branchFrames {
			if fa.ifPos == fb.ifPos && fa.side != fb.side {
				return true
			}
		}
	}
	return false
}

// isTerminatingStmt returns true for statements that exit the enclosing flow
// (return, break, continue, goto, panic).
func isTerminatingStmt(s ast.Stmt) bool {
	switch v := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return v.Tok != token.FALLTHROUGH
	case *ast.ExprStmt:
		if ce, ok := v.X.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
		}
	}
	return false
}

// appendChanOpsFromExpr extracts channel ops from a single expression.
func appendChanOpsFromExpr(expr ast.Expr, stack []branchFrame, exitsAfter bool, ops *[]chanOp) {
	ast.Inspect(expr, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if isCloseCall(n) {
				if chName := channelNameFromArg(n.Args[0]); chName != "" {
					*ops = append(*ops, chanOp{op: "close", name: chName, pos: n.Pos(), exitsAfter: exitsAfter, branchFrames: append([]branchFrame(nil), stack...)})
				}
			}
		case *ast.SendStmt:
			if chName := channelNameFromExpr(n.Chan); chName != "" {
				*ops = append(*ops, chanOp{op: "send", name: chName, pos: n.Pos(), exitsAfter: exitsAfter, branchFrames: append([]branchFrame(nil), stack...)})
			}
		}
		return true
	})
}

// walkStmtsForChanOps walks statements maintaining the if/else branch stack and
// computing exitsAfter (next sibling statement terminates) for each op (#2776).
func walkStmtsForChanOps(stmts []ast.Stmt, stack []branchFrame, ops *[]chanOp) {
	for i, s := range stmts {
		exitsAfter := i+1 < len(stmts) && isTerminatingStmt(stmts[i+1])
		switch v := s.(type) {
		case *ast.DeferStmt:
			// Deferred closes execute at function return — after all sends.
			ast.Inspect(v.Call, func(inner ast.Node) bool {
				if ce, ok := inner.(*ast.CallExpr); ok && isCloseCall(ce) {
					if chName := channelNameFromArg(ce.Args[0]); chName != "" {
						*ops = append(*ops, chanOp{op: "close", name: chName, pos: ce.Pos(), deferred: true, branchFrames: append([]branchFrame(nil), stack...)})
					}
				}
				return true
			})
		case *ast.IfStmt:
			appendChanOpsFromExpr(v.Cond, stack, exitsAfter, ops)
			walkStmtsForChanOps(v.Body.List, append(stack, branchFrame{ifPos: v.Pos(), side: 0}), ops)
			if v.Else != nil {
				if eb, ok := v.Else.(*ast.BlockStmt); ok {
					walkStmtsForChanOps(eb.List, append(stack, branchFrame{ifPos: v.Pos(), side: 1}), ops)
				} else if ei, ok := v.Else.(*ast.IfStmt); ok { // else-if chain
					walkStmtsForChanOps([]ast.Stmt{ei}, append(stack, branchFrame{ifPos: v.Pos(), side: 1}), ops)
				}
			}
		case *ast.GoStmt:
			// Nested func literals (goroutines) execute in the same textual
			// scope for detection purposes — walk their bodies with a fresh
			// branch stack (control-flow frames don't cross function bounds).
			if fl, ok := v.Call.Fun.(*ast.FuncLit); ok && fl.Body != nil {
				walkStmtsForChanOps(fl.Body.List, nil, ops)
			}
		case *ast.ForStmt:
			if v.Body != nil {
				walkStmtsForChanOps(v.Body.List, stack, ops)
			}
		case *ast.RangeStmt:
			if v.Body != nil {
				walkStmtsForChanOps(v.Body.List, stack, ops)
			}
		case *ast.SwitchStmt:
			if v.Body != nil {
				for _, cc := range v.Body.List {
					if cl, ok := cc.(*ast.CaseClause); ok {
						walkStmtsForChanOps(cl.Body, stack, ops)
					}
				}
			}
		case *ast.SelectStmt:
			if v.Body != nil {
				for _, cc := range v.Body.List {
					if cl, ok := cc.(*ast.CommClause); ok {
						walkStmtsForChanOps(cl.Body, stack, ops)
					}
				}
			}
		default:
			// close(ch) and ch <- v are statements in Go — extract from
			// simple statements (ExprStmt / SendStmt).
			collectStmtOps(s, stack, exitsAfter, ops)
		}
	}
}

// collectStmtOps extracts channel ops from a single simple statement: an
// ExprStmt wrapping close(ch), or a bare SendStmt ch <- v.
func collectStmtOps(s ast.Stmt, stack []branchFrame, exitsAfter bool, ops *[]chanOp) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		if ce, ok := v.X.(*ast.CallExpr); ok && isCloseCall(ce) {
			if chName := channelNameFromArg(ce.Args[0]); chName != "" {
				*ops = append(*ops, chanOp{op: "close", name: chName, pos: ce.Pos(), exitsAfter: exitsAfter, branchFrames: append([]branchFrame(nil), stack...)})
			}
		}
	case *ast.SendStmt:
		if chName := channelNameFromExpr(v.Chan); chName != "" {
			*ops = append(*ops, chanOp{op: "send", name: chName, pos: v.Pos(), exitsAfter: exitsAfter, branchFrames: append([]branchFrame(nil), stack...)})
		}
	}
}

// analyzeChannelOpsInFunc inspects a function body for channel safety issues.
func analyzeChannelOpsInFunc(fset *token.FileSet, body *ast.BlockStmt) []channelSafetyInstance {
	var instances []channelSafetyInstance

	// Collect all close() calls and their channels, in source order, with
	// control-flow context (#2776): exitsAfter + if/else branch frames.
	var ops []chanOp
	walkStmtsForChanOps(body.List, nil, &ops)

	// Build per-channel operation sequences.
	chanOps := make(map[string][]chanOp)
	for _, op := range ops {
		chanOps[op.name] = append(chanOps[op.name], op)
	}

	for chName, copList := range chanOps {
		instances = append(instances, detectDoubleClose(fset, chName, copList)...)
		instances = append(instances, detectSendAfterClose(fset, chName, copList)...)
	}

	// Detect close(ch) inside loops where ch is not recreated per iteration.
	instances = append(instances, detectCloseInLoops(fset, body, ops)...)

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
// in the same function scope. #2776: a close followed by a terminating
// statement (return/break/panic) cannot pair with a later close, and two
// closes on different sides of the same if/else are mutually exclusive —
// both are safe Go idioms, not double-close bugs.
func detectDoubleClose(fset *token.FileSet, chName string, ops []chanOp) []channelSafetyInstance {
	var instances []channelSafetyInstance
	var prevClose *chanOp
	for i := range ops {
		op := ops[i]
		if op.op != "close" || op.deferred {
			continue
		}
		if prevClose != nil && !prevClose.exitsAfter && !mutuallyExclusiveOps(*prevClose, op) {
			instances = append(instances, channelSafetyInstance{
				posStr:  fset.Position(op.pos).String(),
				channel: chName,
				kind:    "double-close",
			})
		}
		if prevClose == nil || !prevClose.exitsAfter {
			// Keep tracking unless the earlier close provably exits the flow.
			cp := op
			prevClose = &cp
		}
	}
	return instances
}

// detectSendAfterClose flags when a send appears after a close on the same
// channel in the same function scope (by source order). #2776: a close that
// exits the flow (close+return) and send/close pairs on different sides of
// the same if/else cannot execute in that order — skip them.
func detectSendAfterClose(fset *token.FileSet, chName string, ops []chanOp) []channelSafetyInstance {
	closed := false
	var lastClose chanOp
	for _, op := range ops {
		if op.op == "close" {
			// Deferred closes execute at function return — AFTER all sends.
			// They cannot cause send-after-close panics.
			if op.deferred {
				continue
			}
			if !op.exitsAfter {
				closed = true
				lastClose = op
			}
			continue
		}
		if closed && op.op == "send" && !mutuallyExclusiveOps(lastClose, op) {
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
// #2776: close followed by a terminating statement (close+return producer
// exit) never reaches a second iteration — skip it.
func detectCloseInLoops(fset *token.FileSet, body *ast.BlockStmt, ops []chanOp) []channelSafetyInstance {
	var instances []channelSafetyInstance
	seen := make(map[token.Pos]bool)
	// exitsAfterByPos: fast lookup of control-flow context per close op.
	exitsAfterByPos := make(map[token.Pos]bool, len(ops))
	for _, op := range ops {
		if op.op == "close" {
			exitsAfterByPos[op.pos] = op.exitsAfter
		}
	}

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

		ast.Inspect(loopBody, func(inner ast.Node) bool {
			ce, ok := inner.(*ast.CallExpr)
			if !ok || !isCloseCall(ce) || seen[ce.Pos()] {
				return true
			}
			chName := channelNameFromArg(ce.Args[0])
			if chName == "" || createdInLoop[chName] {
				return true
			}
			if exitsAfterByPos[ce.Pos()] {
				// close+return/break: the loop exits before a second close (#2776).
				return true
			}
			seen[ce.Pos()] = true
			instances = append(instances, channelSafetyInstance{
				posStr:  fset.Position(ce.Pos()).String(),
				channel: chName,
				kind:    "close-in-loop",
			})
			return true
		})
		return true
	})

	return instances
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
