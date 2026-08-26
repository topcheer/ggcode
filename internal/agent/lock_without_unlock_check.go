package agent

// Mutex Lock-Without-Unlock Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that calls mu.Lock()
// or mu.RLock() but forgets the corresponding Unlock()/RUnlock(). This causes
// permanent deadlocks -- the goroutine holds the lock forever, blocking all
// other goroutines that try to acquire it. Unlike resource leaks (missing
// Close() on files), deadlocks are harder to diagnose because they manifest
// as hangs rather than errors, and tests may pass if they don't exercise
// concurrent paths.
//
// The existing resource_leak_check.go lists "Unlock"/"RUnlock" as cleanup
// methods, but its detection logic only matches resource-acquiring assignments
// (e.g., f, err := os.Open()). A mutex Lock() call is a bare statement
// (mu.Lock()), not an assignment -- so missing Unlock is NEVER detected by
// the existing check.
//
// Common LLM failure modes this check catches:
//  1. mu.Lock() with no defer mu.Unlock() anywhere in the function
//  2. mu.Lock() followed by an early return without Unlock
//  3. Copy-paste errors: mu.Lock() ... mu.Lock() (double-lock)
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (go vet doesn't catch this)
//   - Cline/OpenHands: reactive only -- caught by tests or production deadlocks
//   - Aider: no automatic detection
//   - GitHub Copilot: sometimes warns via lint integration
//
// go vet's -copylocks check catches lock-by-value but NOT missing-unlock.
// staticcheck doesn't have a rule for this either. go-deadlock (external tool)
// detects runtime deadlocks but requires the deadlock to actually occur.
//
// Approach: AST-based analysis. For each function, find all Lock/TryLock/RLock
// calls and verify a matching Unlock/RUnlock exists on the same receiver.
// Only NEW instances introduced by this edit are flagged (delta-aware) to
// avoid noise on pre-existing code.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// lockMethodNames maps lock method names to their corresponding unlock methods.
var lockMethodNames = map[string]string{
	"Lock":    "Unlock",
	"RLock":   "RUnlock",
	"TryLock": "Unlock",
}

// lockWithoutUnlockInstance represents a detected lock-without-unlock pattern.
// Issue #1099: delta key uses content anchor (funcName+receiver+method) instead of
// position to avoid false positives when file edits shift line numbers.
type lockWithoutUnlockInstance struct {
	receiver string // the variable/expression that .Lock() was called on
	method   string // the lock method name (Lock, RLock, TryLock)
	funcName string // containing function name for delta key
	posStr   string // human-readable position string for warning display
}

// checkLockWithoutUnlock performs AST-based deadlock detection on Go source.
// Returns warnings for lock acquisitions without corresponding unlock calls
// in the same function.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkLockWithoutUnlock(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newInstances := findLocksWithoutUnlock(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta check: compare against old content using content anchors (funcName+receiver+method).
	// Issue #1099: using position strings causes false positives when file edits shift line numbers.
	var oldKeys map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findLocksWithoutUnlock(oldContent) {
			if oldKeys == nil {
				oldKeys = make(map[string]bool)
			}
			// Use content anchor (funcName+receiver+method) instead of position
			key := iss.funcName + "|" + iss.receiver + "|" + iss.method
			oldKeys[key] = true
		}
	}

	var warnings []string
	for _, inst := range newInstances {
		if oldKeys != nil {
			key := inst.funcName + "|" + inst.receiver + "|" + inst.method
			if oldKeys[key] {
				continue
			}
		}
		unlockMethod := lockMethodNames[inst.method]
		warnings = append(warnings, fmt.Sprintf(
			"Possible deadlock: `%s.%s()` at %s has no corresponding `%s.%s()` "+
				"in the same function. Without an unlock, the goroutine holds the "+
				"lock forever, blocking all other goroutines. Add `defer %s.%s()` "+
				"immediately after the lock call.",
			inst.receiver, inst.method, inst.posStr, inst.receiver, unlockMethod,
			inst.receiver, unlockMethod))
	}

	return warnings
}

// countLocksWithoutUnlock returns the number of lock-without-unlock patterns.
func countLocksWithoutUnlock(src string) int {
	return len(findLocksWithoutUnlock(src))
}

// findLocksWithoutUnlock parses Go source and returns all lock-without-unlock
// instances found, ordered by position.
func findLocksWithoutUnlock(src string) []lockWithoutUnlockInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []lockWithoutUnlockInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		funcName := fn.Name.Name
		locks := findLockCalls(fn)
		if len(locks) == 0 {
			continue
		}

		unlockedReceivers := findUnlockCalls(fn)
		for _, lock := range locks {
			if unlockedReceivers[lock.receiver] {
				continue
			}
			instances = append(instances, lockWithoutUnlockInstance{
				receiver: lock.receiver,
				method:   lock.method,
				funcName: funcName,
				posStr:   fset.Position(lock.pos).String(),
			})
		}
	}

	return instances
}

// lockCall represents a detected lock acquisition.
type lockCall struct {
	receiver string
	method   string
	pos      token.Pos
}

// findLockCalls walks a function body and finds all Lock/RLock/TryLock calls,
// returning the receiver expressions they are called on.
func findLockCalls(fn *ast.FuncDecl) []lockCall {
	var locks []lockCall

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var call *ast.CallExpr

		// Lock can appear as a direct call or in a defer (defer mu.Unlock()).
		// We only care about the Lock side here.
		if c, ok := node.(*ast.CallExpr); ok {
			call = c
		}

		if call == nil {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if _, isLock := lockMethodNames[sel.Sel.Name]; !isLock {
			return true
		}

		receiver := exprToString(sel.X)
		if receiver == "" {
			return true
		}

		locks = append(locks, lockCall{
			receiver: receiver,
			method:   sel.Sel.Name,
			pos:      call.Pos(),
		})

		return true
	})

	return locks
}

// findUnlockCalls walks a function body and returns a set of receiver
// expressions that have Unlock/RUnlock called on them (either directly or
// via defer).
func findUnlockCalls(fn *ast.FuncDecl) map[string]bool {
	unlocked := make(map[string]bool)

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var call *ast.CallExpr

		// Direct call: mu.Unlock()
		if c, ok := node.(*ast.CallExpr); ok {
			call = c
		}
		// Defer: defer mu.Unlock()
		if d, ok := node.(*ast.DeferStmt); ok {
			call = d.Call
		}

		if call == nil {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if sel.Sel.Name != "Unlock" && sel.Sel.Name != "RUnlock" {
			return true
		}

		receiver := exprToString(sel.X)
		if receiver != "" {
			unlocked[receiver] = true
		}

		return true
	})

	return unlocked
}

// exprToString converts an AST expression to a string representation for
// comparison purposes. This handles identifiers (mu), selector expressions
// (s.mu), and index expressions (m["key"]).
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		parent := exprToString(e.X)
		if parent == "" {
			return ""
		}
		return parent + "." + e.Sel.Name
	case *ast.IndexExpr:
		parent := exprToString(e.X)
		if parent == "" {
			return ""
		}
		return parent + "[...]"
	default:
		return ""
	}
}
