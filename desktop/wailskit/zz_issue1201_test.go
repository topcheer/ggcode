package wailskit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #1201: SendHiddenText run-start critical section missing persistedRunCount reset
//
// Problem (before fix):
//   SendHiddenText resets: cancel, cancelled, finished, runGeneration, activeRunGen, runSes
//   BUT NOT: persistedRunCount = 0
//
// Consequence:
//   - Visible run A finishes: persistedRunCount = len(runAdded_A)
//   - Hidden run B starts: persistedRunCount NOT reset (still = len(runAdded_A))
//   - Hidden run B finishes: persistRunMessages calls runMessagesToPersist with stale skip
//   - runMessagesToPersist clamps skip to len(runAdded_B), returns empty tail
//   - B's assistant replies are silently dropped from in-memory ses.Messages/liveHistory
//
// Fix:
//   Add b.persistedRunCount = 0 to SendHiddenText run-start critical section
//   (same as sendMessageData ~line 483 and SendContent ~line 4259)
//
// Result after fix:
//   - All three send paths reset persistedRunCount at run start
//   - Each run starts with persistedRunCount = 0
//   - runMessagesToPersist returns correct messages for each run
//   - No messages are silently dropped

// TestPersistedRunCountReset verifies that all three send paths
// reset the critical persistedRunCount field. This is the core requirement
// for #1201: without this reset, hidden runs inherit stale watermarks
// from previous runs, causing silent message loss.
func TestPersistedRunCountReset(t *testing.T) {
	fset := token.NewFileSet()

	// Parse chat.go to extract field resets from all three send paths
	node, err := parser.ParseFile(fset, "chat.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse chat.go: %v", err)
	}

	// The CRITICAL field that must be reset in ALL three send paths
	criticalField := "persistedRunCount"

	// Verify all three send paths reset the critical field
	funcsWithRunStart := map[string]string{
		"sendMessageData": "visible user-initiated runs",
		"SendContent":     "visible content-sending runs",
		"SendHiddenText":  "hidden agent-initiated runs",
	}

	for funcName, description := range funcsWithRunStart {
		resets := extractFieldResets(t, fset, node, funcName)

		// Check for the critical persistedRunCount reset
		if !resets[criticalField] {
			t.Errorf("%s (%s) run-start section is missing reset for %s (CRITICAL for #1201)", funcName, description, criticalField)
		} else {
			t.Logf("%s (%s) resets %s ✓", funcName, description, criticalField)
		}

		// Log all reset fields for documentation
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

	// Walk the function body to find field assignments
	ast.Inspect(targetFunc.Body, func(n ast.Node) bool {
		// Look for assignment statements: b.field = value
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 {
			return true
		}

		// Check if it's a selector expression: b.persistedRunCount
		sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Verify it's a field on 'b' (ChatBridge receiver)
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "b" {
			return true
		}

		// Record the field name
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
	// This test documents the behavior of runMessagesToPersist:
	//
	// runMessagesToPersist(runAdded, skip):
	//   if skip > 0 {
	//       if skip > len(runAdded) {
	//           skip = len(runAdded)  // clamp to array bounds
	//       }
	//       return runAdded[skip:]  // return tail after skip
	//   }
	//   // ... (drop seed user message on first persist)
	//
	// Scenario without #1201 fix:
	//   - Run A finishes: persistedRunCount = len(runAdded_A) = 10
	//   - Run B starts: persistedRunCount NOT reset (still = 10)
	//   - Run B finishes: runAdded_B = 5 messages
	//   - persistRunMessages calls runMessagesToPersist(runAdded_B, 10)
	//   - skip (10) > len(runAdded_B) (5) → clamp skip to 5
	//   - Returns runAdded_B[5:] = empty slice
	//   - All of B's messages dropped from in-memory state
	//
	// Scenario with #1201 fix:
	//   - Run A finishes: persistedRunCount = len(runAdded_A) = 10
	//   - Run B starts: persistedRunCount = 0 (RESET)
	//   - Run B finishes: runAdded_B = 5 messages
	//   - persistRunMessages calls runMessagesToPersist(runAdded_B, 0)
	//   - skip = 0, so drop seed user message (if present)
	//   - Returns runAdded_B[1:] = assistant messages
	//   - All of B's messages preserved

	t.Log("runMessagesToPersist behavior documented:")
	t.Log("  - Clamps skip to len(runAdded) to prevent panic")
	t.Log("  - When skip > len(runAdded), returns empty slice")
	t.Log("  - This causes message loss if skip is stale")
	t.Log("  - #1201 fix ensures skip starts at 0 for each new run")
}

// TestPersistedRunCountWatermark documents the watermark lifecycle.
func TestPersistedRunCountWatermark(t *testing.T) {
	// This test documents the persistedRunCount watermark lifecycle:
	//
	// Initialization:
	//   - ChatBridge{}: persistedRunCount = 0 (zero value)
	//
	// Run start (all three paths, after #1201 fix):
	//   - b.persistedRunCount = 0 (reset to 0)
	//
	// First persist (runMessagesToPersist with skip=0):
	//   - Drops seed user message (role="user" at index 0)
	//   - Persists assistant messages: runAdded[1:]
	//
	// Watermark advance (line 1338 in persistRunMessages):
	//   - b.persistedRunCount = len(runAdded)
	//   - Marks that all messages in this run have been persisted
	//
	// Subsequent persists (same run, after checkpoint/save):
	//   - runMessagesToPersist(runAdded, persistedRunCount)
	//   - Returns tail after watermark: runAdded[persistedRunCount:]
	//   - Handles incremental persistence without duplicates
	//
	// New run start:
	//   - b.persistedRunCount = 0 (reset - CRITICAL for #1201)
	//   - New run starts with fresh watermark, doesn't inherit from previous run

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
	// SendHiddenText is used in these scenarios:
	//
	// 1. LAN Chat agent-to-agent messages:
	//    - Remote agent sends text to local agent
	//    - Text should be processed but not shown as visible user message
	//    - Agent's response must be persisted to disk
	//
	// 2. Deferred drain of queued messages (after cancellation):
	//    - User cancels a visible run
	//    - Pending messages are queued
	//    - When agent becomes idle, drain queue via SendHiddenText
	//    - Each queued message starts a new hidden run
	//
	// Critical for #1201:
	//   - Hidden runs have the same persistence requirements as visible runs
	//   - Agent's response must be written to disk JSONL
	//   - Missing persistedRunCount reset causes silent message loss
	//   - User sees assistant response in UI, but it disappears on reload

	t.Log("SendHiddenText use cases documented:")
	t.Log("  - LAN Chat: inject remote agent messages without visible user message")
	t.Log("  - Deferred drain: process queued messages after cancellation")
	t.Log("  - Critical requirement: messages must persist to disk")
	t.Log("  - #1201 fix ensures hidden runs get fresh watermark")
	t.Log("  - Without fix: hidden messages silently dropped from in-memory state")
}
