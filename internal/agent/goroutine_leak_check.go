package agent

// Goroutine Lifecycle Leak Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that spawns goroutines
// via `go func()` or `go someFunc()` without any mechanism to track, cancel,
// or wait for their completion. This causes goroutine leaks -- the goroutines
// outlive the function that spawned them, holding references to resources and
// preventing garbage collection. In production, this manifests as slow memory
// growth, file descriptor exhaustion, or mysterious hangs.
//
// The existing resource_leak_check.go detects resource acquisitions (os.Open,
// http.Get) without defer Close(). The lock_without_unlock_check detects mutex
// deadlocks. Neither detects goroutine lifecycle problems.
//
// Common LLM failure modes this check catches:
//  1. `go process()` with no WaitGroup, channel, or context to signal completion
//  2. Fire-and-forget goroutines that hold references to resources (locks,
//     files, connections) that will never be released
//  3. Goroutines spawned in a loop without bounded concurrency
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (go vet doesn't catch this)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - GitHub Copilot: sometimes warns via lint integration
//
// go vet does NOT detect goroutine leaks. staticcheck doesn't either. The
// `go.uber.org/goleak` package detects leaks at test time but requires actual
// execution. This check provides zero-dependency static detection in <1ms.
//
// Approach: AST-based analysis. For each function, find all `go` statements
// and check whether the spawning function has any goroutine lifecycle management:
//   - sync.WaitGroup (Add/Done/Wait)
//   - context.WithCancel/WithTimeout/WithDeadline
//   - errgroup.WithContext
//   - Channel-based signaling (close(ch) or ch <- struct{})
// If NONE of these are present, the goroutine is flagged as a potential leak.
// Only NEW instances introduced by this edit are flagged (delta-aware) to
// avoid noise on pre-existing code.
//
// False positive mitigation:
//   - Functions that already use WaitGroup, errgroup, context cancellation,
//     or channel signaling are NOT flagged even if they contain go statements.
//   - The main() and init() functions are excluded (they are entry points,
//     not expected to wait for goroutines).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// goroutineLeakInstance represents a detected goroutine without lifecycle management.
type goroutineLeakInstance struct {
	posStr string // human-readable position string
}

// checkGoroutineLeak performs AST-based goroutine leak detection on Go source.
// Returns warnings for goroutine spawns without synchronization mechanisms.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkGoroutineLeak(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Delta-aware: count patterns in old content.
	oldCount := countGoroutineLeaks(oldContent)

	newInstances := findGoroutineLeaks(newContent)
	if len(newInstances) <= oldCount {
		return nil // no new instances introduced
	}

	// Flag only newly introduced instances.
	newCount := len(newInstances) - oldCount
	var warnings []string
	for i := 0; i < newCount && i+oldCount < len(newInstances); i++ {
		inst := newInstances[oldCount+i]
		warnings = append(warnings, fmt.Sprintf(
			"Possible goroutine leak: `go` statement at %s has no lifecycle management "+
				"(no WaitGroup, context cancellation, errgroup, or channel signaling) "+
				"in the spawning function. The goroutine will outlive the function scope, "+
				"leaking resources. Add a sync.WaitGroup, context.WithCancel, or use "+
				"golang.org/x/sync/errgroup to track goroutine completion.",
			inst.posStr))
	}

	return warnings
}

// countGoroutineLeaks returns the number of goroutine leak patterns.
func countGoroutineLeaks(src string) int {
	return len(findGoroutineLeaks(src))
}

// findGoroutineLeaks parses Go source and returns all goroutine leak instances.
func findGoroutineLeaks(src string) []goroutineLeakInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []goroutineLeakInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Skip main() and init() -- they are program entry points, not
		// expected to join goroutines.
		if fn.Name != nil && (fn.Name.Name == "main" || fn.Name.Name == "init") {
			continue
		}

		// First check if this function has ANY goroutine lifecycle management.
		// If it does, all go statements in it are considered safe.
		if hasGoroutineLifecycle(fn.Body) {
			continue
		}

		// Find all go statements without lifecycle management.
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			goStmt, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}

			instances = append(instances, goroutineLeakInstance{
				posStr: fset.Position(goStmt.Pos()).String(),
			})
			return true
		})
	}

	return instances
}

// hasGoroutineLifecycle returns true if the function body contains any
// goroutine lifecycle management mechanism.
func hasGoroutineLifecycle(body *ast.BlockStmt) bool {
	found := false

	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		if isLifecycleNode(node) {
			found = true
			return false
		}
		return true
	})

	return found
}

// waitGroupMethods are sync.WaitGroup / errgroup method names that indicate
// goroutine lifecycle management.
var waitGroupMethods = map[string]bool{
	"Wait": true, "Done": true, "Add": true,
}

// contextDeriveMethods are context package functions that create cancellable
// contexts, indicating goroutine lifecycle management.
var contextDeriveMethods = map[string]bool{
	"WithCancel": true, "WithTimeout": true, "WithDeadline": true,
	"WithCancelCause": true, "WithTimeoutCause": true, "WithDeadlineCause": true,
	"WithContext": true, // errgroup.WithContext
}

// signalChannelKeywords are keywords in channel variable names that suggest
// the channel is used for goroutine signaling (shutdown/cancel).
var signalChannelKeywords = []string{"stop", "done", "quit", "shutdown", "cancel", "exit", "term"}

// isLifecycleNode returns true if the AST node represents a goroutine
// lifecycle management mechanism.
func isLifecycleNode(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.SelectorExpr:
		// sync.WaitGroup methods: Add, Done, Wait.
		return n.Sel != nil && waitGroupMethods[n.Sel.Name]

	case *ast.CallExpr:
		// context.WithCancel/WithTimeout/WithDeadline, errgroup.WithContext.
		if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil {
			return contextDeriveMethods[sel.Sel.Name]
		}
		// close(ch) -- channel close is a signaling mechanism.
		if ident, ok := n.Fun.(*ast.Ident); ok {
			return ident.Name == "close"
		}

	case *ast.SendStmt:
		// Channel send to a signal-like channel name.
		if ident, ok := n.Chan.(*ast.Ident); ok {
			lower := strings.ToLower(ident.Name)
			for _, kw := range signalChannelKeywords {
				if strings.Contains(lower, kw) {
					return true
				}
			}
		}
	}
	return false
}
