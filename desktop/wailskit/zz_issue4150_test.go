package wailskit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #4150: run-end defer queue-pump asymmetry
//
// Problem (before fix):
//   chat.go has three run-starter functions (sendMessageData, SendContent,
//   SendHiddenText). The first two end with a defer that drains ONE pending
//   queued message (pumping the FIFO chain), but SendHiddenText's defer only
//   cleared b.cancel — no drain.
//
// Consequence:
//   - Run busy: LAN hidden message queued, then desktop visible message queued
//   - Run ends: defer pops the hidden message, hidden run starts
//   - Hidden run ends: its defer does NOT drain -> the visible message stays
//     stranded in the queue indefinitely (UI idle, queue non-empty) until the
//     next user action (next send or PopLastVisible) happens to consume it.
//
// Fix:
//   SendHiddenText's run-end defer now mirrors the other two: it calls
//   b.drainPending() and routes the popped message (hidden -> SendHiddenText,
//   visible -> sendMessageData with its FIFO-aligned source/exclude pair).
//
// Result after fix:
//   - All three run-end defers pump the queue symmetrically
//   - A hidden message at the head can no longer break the pump chain

// TestRunEndDefersDrainSymmetry verifies that all three run-starter functions
// drain the pending queue in their run-end defer. This is the core invariant
// for #4150: any future run-starter that forgets the drain re-introduces the
// stranded-queue bug.
func TestRunEndDefersDrainSymmetry(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "chat.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse chat.go: %v", err)
	}

	funcsWithRunEnd := map[string]string{
		"sendMessageData": "visible user-initiated runs",
		"SendContent":     "visible content-sending runs",
		"SendHiddenText":  "hidden agent-initiated runs",
	}

	for funcName, description := range funcsWithRunEnd {
		if !hasDeferredDrain(t, node, funcName) {
			t.Errorf("%s (%s) run-end defer is missing the b.drainPending() pump (CRITICAL for #4150)", funcName, description)
		} else {
			t.Logf("%s (%s) drains pending queue in run-end defer ✓", funcName, description)
		}
	}
}

// hasDeferredDrain reports whether funcName's body contains a defer statement
// (typically `defer func() { ... }()`) whose body calls b.drainPending().
func hasDeferredDrain(t *testing.T, node *ast.File, funcName string) bool {
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			d, ok := n.(*ast.DeferStmt)
			if !ok {
				return true
			}
			// Handle `defer func() { ... }()` and `defer b.cleanup()` forms.
			var scope ast.Node = d.Call
			if fl, ok := d.Call.Fun.(*ast.FuncLit); ok {
				scope = fl.Body
			}
			ast.Inspect(scope, func(m ast.Node) bool {
				if call, ok := m.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "drainPending" {
						found = true
					}
				}
				return !found
			})
			return !found
		})
		return found
	}
	return false
}

// TestHiddenThenVisibleQueueInterleave documents the #4150 trigger ordering at
// the queue level: while a run is busy, a hidden (LAN peer) message and a
// visible (desktop user) message queue up FIFO — hidden first. Every run-end
// drain must pop exactly one entry in that order; a hidden-head break of the
// pump chain is exactly this sequence failing to reach the visible entry.
func TestHiddenThenVisibleQueueInterleave(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}

	// Busy-run interleaved enqueue (issue timeline steps 2-3): LAN hidden
	// message lands first, desktop visible message second.
	b.mu.Lock()
	b.pendingMsgs.Enqueue("lan hidden payload", true, nil)
	b.pendingMsgs.Enqueue("stranded visible question", false, nil)
	b.pendingSource = append(b.pendingSource, "desktop")
	b.pendingExclude = append(b.pendingExclude, "")
	b.mu.Unlock()

	// First run-end drain pops the hidden head.
	p1, ok := b.drainPending()
	if !ok || !p1.Hidden || p1.Text != "lan hidden payload" {
		t.Fatalf("first drain must pop the hidden head, got ok=%v hidden=%v text=%q", ok, p1.Hidden, p1.Text)
	}

	// Hidden runs consume no source/exclude pair (#477 invariant).
	b.mu.Lock()
	srcLen, exclLen := len(b.pendingSource), len(b.pendingExclude)
	b.mu.Unlock()
	if srcLen != 1 || exclLen != 1 {
		t.Fatalf("hidden consume must not pop the parallel pair, got source=%d exclude=%d", srcLen, exclLen)
	}

	// The hidden run's OWN run-end defer (post-fix) must pop the visible
	// entry — the exact pump step that was missing before #4150.
	p2, ok := b.drainPending()
	if !ok || p2.Hidden || p2.Text != "stranded visible question" {
		t.Fatalf("second drain must pop the stranded visible message, got ok=%v hidden=%v text=%q", ok, p2.Hidden, p2.Text)
	}
	b.mu.Lock()
	src := b.pendingSource[0]
	excl := b.pendingExclude[0]
	b.pendingSource = b.pendingSource[1:]
	b.pendingExclude = b.pendingExclude[1:]
	b.mu.Unlock()
	if src != "desktop" || excl != "" {
		t.Fatalf("visible message must drain with its OWN pair, got source=%q exclude=%q", src, excl)
	}

	// Queue fully pumped — nothing stranded.
	if _, ok := b.drainPending(); ok {
		t.Fatalf("queue must be empty after two symmetric run-end drains")
	}
}
