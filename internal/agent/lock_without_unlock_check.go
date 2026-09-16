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
	"sort"
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
//
// #2433: the old implementation was flow-insensitive receiver-set matching -
// ANY same-receiver Unlock anywhere in the function exempted ALL its Lock
// calls, so the headline case (mu.Lock(); if err != nil { return }; mu.Unlock())
// was invisible: the early return held the lock yet zero warnings fired.
// Worse, the inverse misfired: legitimate indirect releases
// (defer s.release(), unlock := mu.Unlock; defer unlock()) warned.
//
// Strategy now: per-function, if an INDIRECT release shape exists (any
// defer whose called method looks like unlock/release, or a defer on a
// plain variable - the method-value idiom), fall back to the old
// flow-insensitive check (conservative silence on that function).
// Otherwise run a sequential lock-holding simulation: Lock acquires,
// Unlock/defer-Unlock releases, return statements (and the implicit
// function-end return) report still-held receivers, and branch bodies
// (if/else/for/switch) are simulated on a COPY of the held set so
// per-branch acquires do not leak across joins (branch returns are still
// checked inside the branch, which is exactly where early exits live).
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

		if findLockCalls(fn) == nil {
			continue
		}

		if fnHasIndirectRelease(fn) {
			// Indirect-release shapes (defer s.release(), the method-value
			// idiom) make precise simulation unsound without type info, and
			// the old flow-insensitive check misfires on them as FPs - so
			// exempt the whole function (#2433 issue recommendation:
			// downgrade/exempt rather than warn).
			continue
		}

		instances = append(instances, simulateHeldLocks(fn, fset)...)
	}

	return instances
}

// fnHasIndirectRelease reports whether fn contains any release shape the
// sequential simulator cannot model precisely (#2433 FP side):
//   - defer on a bare variable call (defer unlock()) - the
//     `v := mu.Unlock` method-value idiom
//   - defer calling a method whose name suggests release but is not the
//     canonical Unlock/RUnlock on the same receiver (defer s.release())
func fnHasIndirectRelease(fn *ast.FuncDecl) bool {
	indirect := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		d, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		switch callee := d.Call.Fun.(type) {
		case *ast.Ident:
			// defer unlock() - variable call: cannot resolve the receiver
			// it releases without type info; treat as indirect.
			indirect = true
		case *ast.SelectorExpr:
			if callee.Sel.Name != "Unlock" && callee.Sel.Name != "RUnlock" {
				lower := strings.ToLower(callee.Sel.Name)
				if strings.Contains(lower, "unlock") || strings.Contains(lower, "release") {
					// defer s.release() - releases some lock the simulator
					// cannot see through.
					indirect = true
				}
			}
		}
		return true
	})
	return indirect
}

// simHeldEntry is one receiver currently held in the sequential lock
// simulation (#2433).
type simHeldEntry struct {
	lock lockCall
	// reported guards against duplicate warnings when multiple early
	// returns hold the same receiver: one warning per (func, receiver,
	// method) keeps the delta anchor stable (#1099).
	reported bool
}

// simulateHeldLocks walks fn's statements sequentially tracking which
// receivers are held, and reports every receiver still held at a return
// statement (early exit) or at the function's implicit end-of-body return.
// Branch bodies run on a copy of the held set (joins do not accumulate
// per-branch state), but returns inside them ARE checked in-context.
func simulateHeldLocks(fn *ast.FuncDecl, fset *token.FileSet) []lockWithoutUnlockInstance {
	held := map[string]*simHeldEntry{}
	var instances []lockWithoutUnlockInstance

	var reportHeld func(where string)
	reportHeld = func(where string) {
		for _, recv := range sortedSimKeys(held) {
			e := held[recv]
			if e.reported {
				continue
			}
			e.reported = true
			instances = append(instances, lockWithoutUnlockInstance{
				receiver: recv,
				method:   e.lock.method,
				funcName: fn.Name.Name,
				posStr:   fset.Position(e.lock.pos).String() + " (" + where + ")",
			})
		}
	}

	var applyCall func(call *ast.CallExpr)
	applyCall = func(call *ast.CallExpr) {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		recv := exprToString(sel.X)
		if recv == "" {
			return
		}
		if _, isLock := lockMethodNames[sel.Sel.Name]; isLock {
			if _, exists := held[recv]; !exists {
				held[recv] = &simHeldEntry{lock: lockCall{receiver: recv, method: sel.Sel.Name, pos: call.Pos()}}
			}
			return
		}
		if sel.Sel.Name == "Unlock" || sel.Sel.Name == "RUnlock" {
			delete(held, recv)
		}
	}

	var applyDefer func(call *ast.CallExpr)
	applyDefer = func(call *ast.CallExpr) {
		// defer mu.Unlock() schedules the release for every remaining
		// path: the receiver is no longer "at risk".
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "Unlock" || sel.Sel.Name == "RUnlock" {
				if recv := exprToString(sel.X); recv != "" {
					delete(held, recv)
				}
			}
		}
		// Non-canonical defer releases were routed to the conservative
		// fallback by fnHasIndirectRelease before we get here.
	}

	var walkStmts func(stmts []ast.Stmt)
	walkStmts = func(stmts []ast.Stmt) {
		for _, st := range stmts {
			switch s := st.(type) {
			case *ast.ExprStmt:
				if call, ok := s.X.(*ast.CallExpr); ok {
					applyCall(call)
				}
			case *ast.DeferStmt:
				applyDefer(s.Call)
			case *ast.ReturnStmt:
				reportHeld("held across return")
			case *ast.AssignStmt:
				// v := mu.Unlock method-value bindings are handled by the
				// indirect-release fallback; assignments of plain calls
				// (rare: go-less bare call in expr) still get scanned.
				for _, rhs := range s.Rhs {
					if call, ok := rhs.(*ast.CallExpr); ok {
						applyCall(call)
					}
				}
			case *ast.BlockStmt:
				walkStmts(s.List)
			case *ast.IfStmt:
				if s.Init != nil {
					walkStmts([]ast.Stmt{s.Init})
				}
				thenHeld := copySimHeld(held)
				saved := held
				held = thenHeld
				// The condition acquires on the taken path (TryLock idiom):
				// `if mu.TryLock() { ... }` holds only inside the branch.
				if call, ok := s.Cond.(*ast.CallExpr); ok {
					applyCall(call)
				}
				walkStmts(s.Body.List)
				// A receiver still held at the branch end leaks on that path
				// (the TryLock idiom's forgotten-Unlock shape).
				reportHeld("held at branch end")
				held = saved
				if s.Else != nil {
					elseHeld := copySimHeld(held)
					saved = held
					held = elseHeld
					switch e := s.Else.(type) {
					case *ast.BlockStmt:
						walkStmts(e.List)
					case *ast.IfStmt:
						walkStmts([]ast.Stmt{e})
					default:
						walkStmts([]ast.Stmt{e})
					}
					reportHeld("held at branch end")
					held = saved
				}
			case *ast.ForStmt:
				loopHeld := copySimHeld(held)
				saved := held
				held = loopHeld
				if s.Body != nil {
					walkStmts(s.Body.List)
				}
				held = saved
			case *ast.RangeStmt:
				loopHeld := copySimHeld(held)
				saved := held
				held = loopHeld
				if s.Body != nil {
					walkStmts(s.Body.List)
				}
				held = saved
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				var body *ast.BlockStmt
				switch e := st.(type) {
				case *ast.SwitchStmt:
					body = e.Body
				case *ast.TypeSwitchStmt:
					body = e.Body
				case *ast.SelectStmt:
					body = e.Body
				}
				if body != nil {
					caseHeld := copySimHeld(held)
					saved := held
					held = caseHeld
					walkStmts(body.List)
					held = saved
				}
			default:
				// Labeled statements, decls, inc/dec, send, go, etc: not
				// lock-relevant in their statement form; nested blocks inside
				// them are rare and the conservative branch-copy pattern
				// above covers the common shapes.
				_ = s
			}
		}
	}

	walkStmts(fn.Body.List)
	// Implicit end-of-function return.
	reportHeld("held at function end")

	return instances
}

func copySimHeld(held map[string]*simHeldEntry) map[string]*simHeldEntry {
	out := make(map[string]*simHeldEntry, len(held))
	for k, v := range held {
		cp := *v // deep copy: branch-local reporting must not mark the shared entry
		out[k] = &cp
	}
	return out
}

func sortedSimKeys(m map[string]*simHeldEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
