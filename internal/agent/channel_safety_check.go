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
	posStr  string // position of the offending statement
	channel string // channel variable name
	kind    string // "double-close", "send-after-close", "close-in-loop"
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
		key := inst.channel + inst.kind
		if oldSet[key] {
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
		instances = append(instances, analyzeChannelOpsInFunc(fset, fn.Body)...)
	}

	return instances
}

// chanOp represents a single channel operation (close or send) found in source.
type chanOp struct {
	op   string // "close" or "send"
	name string
	pos  token.Pos
}

// analyzeChannelOpsInFunc inspects a function body for channel safety issues.
func analyzeChannelOpsInFunc(fset *token.FileSet, body *ast.BlockStmt) []channelSafetyInstance {
	var instances []channelSafetyInstance

	// Collect all close() calls and their channels, in source order.
	var ops []chanOp

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if isCloseCall(n) {
				chName := channelNameFromArg(n.Args[0])
				if chName != "" {
					ops = append(ops, chanOp{op: "close", name: chName, pos: n.Pos()})
				}
			}
		case *ast.SendStmt:
			chName := channelNameFromExpr(n.Chan)
			if chName != "" {
				ops = append(ops, chanOp{op: "send", name: chName, pos: n.Pos()})
			}
		}
		return true
	})

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
func detectDoubleClose(fset *token.FileSet, chName string, ops []chanOp) []channelSafetyInstance {
	var instances []channelSafetyInstance
	closeCount := 0
	for _, op := range ops {
		if op.op != "close" {
			continue
		}
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
func detectSendAfterClose(fset *token.FileSet, chName string, ops []chanOp) []channelSafetyInstance {
	closed := false
	for _, op := range ops {
		if op.op == "close" {
			closed = true
			continue
		}
		if closed && op.op == "send" {
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

		ast.Inspect(loopBody, func(inner ast.Node) bool {
			ce, ok := inner.(*ast.CallExpr)
			if !ok || !isCloseCall(ce) || seen[ce.Pos()] {
				return true
			}
			chName := channelNameFromArg(ce.Args[0])
			if chName == "" || createdInLoop[chName] {
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
func collectChannelSafetyIssues(src string) map[string]bool {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	instances := findChannelSafetyIssues(src)
	result := make(map[string]bool, len(instances))
	for _, inst := range instances {
		result[inst.channel+inst.kind] = true
	}
	return result
}
