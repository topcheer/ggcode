package wailskit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

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

func TestHiddenThenVisibleQueueInterleave(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}

	b.mu.Lock()
	b.pendingMsgs.Enqueue("lan hidden payload", true, nil)
	b.pendingMsgs.Enqueue("stranded visible question", false, nil)
	b.pendingSource = append(b.pendingSource, "desktop")
	b.pendingExclude = append(b.pendingExclude, "")
	b.mu.Unlock()

	p1, ok := b.drainPending()
	if !ok || !p1.Hidden || p1.Text != "lan hidden payload" {
		t.Fatalf("first drain must pop the hidden head, got ok=%v hidden=%v text=%q", ok, p1.Hidden, p1.Text)
	}

	b.mu.Lock()
	srcLen, exclLen := len(b.pendingSource), len(b.pendingExclude)
	b.mu.Unlock()
	if srcLen != 1 || exclLen != 1 {
		t.Fatalf("hidden consume must not pop the parallel pair, got source=%d exclude=%d", srcLen, exclLen)
	}

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

	if _, ok := b.drainPending(); ok {
		t.Fatalf("queue must be empty after two symmetric run-end drains")
	}
}
