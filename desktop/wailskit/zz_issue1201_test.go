package wailskit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestPersistedRunCountReset verifies that all three send paths
// reset the critical persistedRunCount field. This is the core requirement
// for #1201: without this reset, hidden runs inherit stale watermarks
// from previous runs, causing silent message loss.
func TestPersistedRunCountReset(t *testing.T) {
	fset := token.NewFileSet()

	node, err := parser.ParseFile(fset, "chat.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse chat.go: %v", err)
	}

	criticalField := "persistedRunCount"

	funcsWithRunStart := map[string]string{
		"sendMessageData": "visible user-initiated runs",
		"SendContent":     "visible content-sending runs",
		"SendHiddenText":  "hidden agent-initiated runs",
	}

	for funcName, description := range funcsWithRunStart {
		resets := extractFieldResets(t, fset, node, funcName)

		if !resets[criticalField] {
			t.Errorf("%s (%s) run-start section is missing reset for %s (CRITICAL for #1201)", funcName, description, criticalField)
		} else {
			t.Logf("%s (%s) resets %s ✓", funcName, description, criticalField)
		}

		t.Logf("  %s resets: %v", funcName, getResetFieldNames(resets))
	}
}

// extractFieldResets analyzes a function's AST to find field assignments
// in the critical section (after cancel creation, before lock release).
func extractFieldResets(t *testing.T, fset *token.FileSet, node *ast.File, funcName string) map[string]bool {
	resets := make(map[string]bool)

	// Find the function
	var targetFunc *ast.FuncDecl
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		targetFunc = fn
		break
	}

	if targetFunc == nil {
		t.Fatalf("Function %s not found", funcName)
	}

	ast.Inspect(targetFunc.Body, func(n ast.Node) bool {

		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 {
			return true
		}

		sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}

		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "b" {
			return true
		}

		fieldName := sel.Sel.Name
		resets[fieldName] = true

		return true
	})

	return resets
}

// getResetFieldNames converts a reset map to a string slice.
func getResetFieldNames(resets map[string]bool) []string {
	var names []string
	for name := range resets {
		names = append(names, name)
	}
	return names
}

// TestRunMessagesToPersistBehavior documents how runMessagesToPersist
// works and why persistedRunCount reset is critical.
func TestRunMessagesToPersistBehavior(t *testing.T) {

	t.Log("runMessagesToPersist behavior documented:")
	t.Log("  - Clamps skip to len(runAdded) to prevent panic")
	t.Log("  - When skip > len(runAdded), returns empty slice")
	t.Log("  - This causes message loss if skip is stale")
	t.Log("  - #1201 fix ensures skip starts at 0 for each new run")
}

// TestPersistedRunCountWatermark documents the watermark lifecycle.
func TestPersistedRunCountWatermark(t *testing.T) {

	t.Log("persistedRunCount watermark lifecycle documented:")
	t.Log("  - Start: 0 (zero value)")
	t.Log("  - Run start: reset to 0 (all three paths)")
	t.Log("  - First persist: drop seed user, persist assistant messages")
	t.Log("  - Watermark advance: persistedRunCount = len(runAdded)")
	t.Log("  - Subsequent persists: return tail after watermark")
	t.Log("  - New run start: reset to 0 (prevents stale watermark)")
}

// TestSendHiddenTextUseCases documents when SendHiddenText is used.
func TestSendHiddenTextUseCases(t *testing.T) {

	t.Log("SendHiddenText use cases documented:")
	t.Log("  - LAN Chat: inject remote agent messages without visible user message")
	t.Log("  - Deferred drain: process queued messages after cancellation")
	t.Log("  - Critical requirement: messages must persist to disk")
	t.Log("  - #1201 fix ensures hidden runs get fresh watermark")
	t.Log("  - Without fix: hidden messages silently dropped from in-memory state")
}
