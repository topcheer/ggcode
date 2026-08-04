package agent

// Concurrency Race Detection Verification Hint
//
// Research basis: Data races are the #1 source of subtle, non-deterministic
// bugs in concurrent Go programs. They are invisible to static analysis --
// only the Go race detector (`go test -race`) catches them at runtime.
//
// Industry trend: Agent verification loops ("verify, don't assume") -- the
// dominant pattern in 2025-2026 AI agent design (Anthropic's verifiers,
// Cognition/Devin, OpenHands). The agent should proactively verify its
// concurrency changes with the appropriate tool.
//
// Competitor analysis:
//   - Claude Code: no automatic -race suggestion
//   - Cursor: no automatic -race suggestion
//   - Cline/OpenHands: no automatic -race suggestion
//   - Aider: no automatic -race suggestion
//   - GitHub Copilot: sometimes mentions -race in chat, no automated trigger
//
// Gap: ggcode has extensive static concurrency checks (goroutine-leak,
// lock-without-unlock, concurrent-map-access, select-timer-leak), but none
// proactively suggests runtime verification. Static checks catch structural
// issues; the race detector catches temporal issues (concurrent read/write
// to the same variable without synchronization). These are complementary.
//
// This check is delta-aware: it only fires when concurrency primitives are
// NEWLY introduced or MODIFIED in the edit. If the old content already had
// the same concurrency patterns, no hint is generated (avoiding noise on
// reformats or unrelated edits).
//
// Design:
//   - Zero LLM cost (AST-based detection, deterministic)
//   - Fires at most once per edit (no spam)
//   - Only triggers for Go files
//   - Only fires when NEW concurrency code is introduced (delta-aware)
//   - Produces a concise, actionable hint to run `go test -race`

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// concurrencyPrimitive patterns that warrant race detector verification.
// When these are newly introduced in an edit, the race detector should be run.
//
// Detection targets:
//   1. `go` statements (goroutine launches) -- most common race source
//   2. sync.Mutex / sync.RWMutex -- shared state protection
//   3. sync.WaitGroup -- goroutine coordination
//   4. sync.Map -- concurrent map access
//   5. sync.Once -- one-time initialization
//   6. sync.Cond -- condition variables
//   7. sync.Pool -- object pooling
//   8. atomic.* operations -- lock-free concurrency
//   9. channel send/receive in new goroutine context
//
// We deliberately do NOT trigger on:
//   - context.WithCancel/WithTimeout (these are for cancellation, not races)
//   - time.After/Timer (single-goroutine timing)
//   - errgroup (already provides synchronization)

// checkRaceVerifyHint detects newly introduced concurrency primitives and
// suggests running the Go race detector.
func checkRaceVerifyHint(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Skip test files -- they already run under `go test`, and the hint is
	// about verifying the production code they test.
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}

	oldScore := countConcurrencyPrimitives(oldContent)
	newScore := countConcurrencyPrimitives(newContent)

	// Delta-aware: only trigger if new concurrency primitives were introduced.
	if newScore <= oldScore {
		return nil
	}

	return []string{
		"Concurrency code modified -- data races are invisible to static analysis. " +
			"Verify with `go test -race ./...` to catch concurrent read/write hazards " +
			"that compile fine but fail non-deterministically at runtime.",
	}
}

// countConcurrencyPrimitives returns a count of concurrency-relevant patterns
// in Go source code. Uses AST parsing for accuracy.
func countConcurrencyPrimitives(src string) int {
	if strings.TrimSpace(src) == "" {
		return 0
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		// Fall back to simple string matching if AST parsing fails.
		return countConcurrencyPatternsString(src)
	}

	count := 0

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.GoStmt:
			// Goroutine launch -- primary race risk.
			count++

		case *ast.SelectorExpr:
			// Check for sync.* and atomic.* package usage.
			if ident, ok := n.X.(*ast.Ident); ok {
				pkg := ident.Name
				sel := n.Sel.Name
				switch pkg {
				case "sync":
					switch sel {
					case "Mutex", "RWMutex", "WaitGroup", "Map",
						"Once", "Cond", "Pool":
						count++
					}
				case "atomic":
					// Any atomic operation indicates concurrent access.
					count++
				}
			}

		case *ast.SendStmt:
			// Channel send -- can cause races if receiver is concurrent.
			count++
		}
		return true
	})

	return count
}

// countConcurrencyPatternsString is a fallback when AST parsing fails.
// Uses simple substring matching for the most critical patterns.
func countConcurrencyPatternsString(src string) int {
	count := 0
	// Goroutine launches: "go func" or "go someFunc"
	count += strings.Count(src, "go func(") + strings.Count(src, "go func (")
	// Look for "go someFunc(" pattern (word boundary before "go")
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "go ") && !strings.HasPrefix(trimmed, "go func") {
			// Likely a goroutine launch like "go process()"
			if strings.Contains(trimmed, "(") {
				count++
			}
		}
	}
	// sync primitives
	for _, pat := range []string{
		"sync.Mutex", "sync.RWMutex", "sync.WaitGroup",
		"sync.Map", "sync.Once", "sync.Cond", "sync.Pool",
	} {
		count += strings.Count(src, pat)
	}
	// atomic operations
	count += strings.Count(src, "atomic.")
	return count
}
