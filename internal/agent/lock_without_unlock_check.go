package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

var lockMethodNames = map[string]string{
	"Lock":    "Unlock",
	"RLock":   "RUnlock",
	"TryLock": "Unlock",
}

type lockWithoutUnlockInstance struct {
	receiver string
	method   string
	funcName string
	posStr   string
}

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

	var oldKeys map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findLocksWithoutUnlock(oldContent) {
			if oldKeys == nil {
				oldKeys = make(map[string]bool)
			}

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

func findLocksWithoutUnlock(src string) []lockWithoutUnlockInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
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

			continue
		}

		instances = append(instances, simulateHeldLocks(fn, fset)...)
	}

	return instances
}

func fnHasIndirectRelease(fn *ast.FuncDecl) bool {

	methodValues := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != len(assign.Rhs) {
			return true
		}
		for i, rhs := range assign.Rhs {
			sel, ok := rhs.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Unlock" && sel.Sel.Name != "RUnlock") {
				continue
			}
			if id, ok := assign.Lhs[i].(*ast.Ident); ok {
				methodValues[id.Name] = true
			}
		}
		return true
	})

	indirect := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		d, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		switch callee := d.Call.Fun.(type) {
		case *ast.Ident:

			lower := strings.ToLower(callee.Name)
			if methodValues[callee.Name] ||
				strings.Contains(lower, "unlock") || strings.Contains(lower, "release") {
				indirect = true
			}
		case *ast.SelectorExpr:
			if callee.Sel.Name != "Unlock" && callee.Sel.Name != "RUnlock" {
				lower := strings.ToLower(callee.Sel.Name)
				if strings.Contains(lower, "unlock") || strings.Contains(lower, "release") {

					indirect = true
				}
			}
		}
		return true
	})
	return indirect
}

type simHeldEntry struct {
	lock     lockCall
	reported bool
}

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
			if prev, exists := held[recv]; exists {

				if !prev.reported && (sel.Sel.Name == "Lock" || sel.Sel.Name == "TryLock") {
					prev.reported = true
					instances = append(instances, lockWithoutUnlockInstance{
						receiver: recv,
						method:   sel.Sel.Name,
						funcName: fn.Name.Name,
						posStr:   fset.Position(call.Pos()).String() + " (double lock / non-reentrant re-acquire)",
					})
				}
				return
			}
			held[recv] = &simHeldEntry{lock: lockCall{receiver: recv, method: sel.Sel.Name, pos: call.Pos()}}
			return
		}
		if sel.Sel.Name == "Unlock" || sel.Sel.Name == "RUnlock" {
			delete(held, recv)
		}
	}

	var applyDefer func(call *ast.CallExpr)
	applyDefer = func(call *ast.CallExpr) {

		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "Unlock" || sel.Sel.Name == "RUnlock" {
				if recv := exprToString(sel.X); recv != "" {
					delete(held, recv)
				}
			}
		}

	}

	var walkStmts func(stmts []ast.Stmt)

	simBranch := func(body func()) {
		branchHeld := copySimHeld(held)
		saved := held
		held = branchHeld
		body()
		reportHeld("held at branch end")
		held = saved
	}

	switchHasDefault := func(body *ast.BlockStmt) bool {
		if body == nil {
			return false
		}
		for _, cs := range body.List {
			if cl, ok := cs.(*ast.CaseClause); ok && cl.List == nil {
				return true
			}
		}
		return false
	}
	var walkClauseBody func(body *ast.BlockStmt, bypassPossible bool)
	walkClauseBody = func(body *ast.BlockStmt, bypassPossible bool) {
		if body == nil {
			return
		}
		saved := held
		joined := map[string]*simHeldEntry{}
		if bypassPossible {
			joined = copySimHeld(saved)
		}
		for _, cs := range body.List {
			branchHeld := copySimHeld(saved)
			held = branchHeld
			switch cl := cs.(type) {
			case *ast.CaseClause:
				walkStmts(cl.Body)
			case *ast.CommClause:
				if cl.Comm != nil {

					walkStmts([]ast.Stmt{cl.Comm})
				}
				walkStmts(cl.Body)
			default:
				walkStmts([]ast.Stmt{cs})
			}
			reportHeld("held at branch end")
			for recv, e := range branchHeld {
				if _, ok := joined[recv]; !ok {
					joined[recv] = e
				}
			}
		}

		held = saved
		for recv := range saved {
			if _, ok := joined[recv]; !ok {
				delete(saved, recv)
			}
		}
		for recv, e := range joined {
			if outer, ok := saved[recv]; ok {

				if e.reported {
					outer.reported = true
				}
			} else {
				saved[recv] = e
			}
		}
	}
	walkStmts = func(stmts []ast.Stmt) {
		for _, st := range stmts {
			switch s := st.(type) {
			case *ast.ExprStmt:
				if call, ok := s.X.(*ast.CallExpr); ok {
					applyCall(call)

					if fl, ok := call.Fun.(*ast.FuncLit); ok && fl.Body != nil {
						simBranch(func() { walkStmts(fl.Body.List) })
					}
				}
			case *ast.DeferStmt:
				applyDefer(s.Call)
			case *ast.ReturnStmt:
				reportHeld("held across return")
			case *ast.AssignStmt:

				for _, rhs := range s.Rhs {
					if call, ok := rhs.(*ast.CallExpr); ok {
						applyCall(call)
					}

					if fl, ok := rhs.(*ast.FuncLit); ok && fl.Body != nil {
						simBranch(func() { walkStmts(fl.Body.List) })
					}
				}
			case *ast.BlockStmt:
				walkStmts(s.List)
			case *ast.IfStmt:
				if s.Init != nil {
					walkStmts([]ast.Stmt{s.Init})
				}
				simBranch(func() {

					if call, ok := s.Cond.(*ast.CallExpr); ok {
						applyCall(call)
					}
					walkStmts(s.Body.List)
				})
				if s.Else != nil {
					simBranch(func() {
						switch e := s.Else.(type) {
						case *ast.BlockStmt:
							walkStmts(e.List)
						case *ast.IfStmt:
							walkStmts([]ast.Stmt{e})
						default:
							walkStmts([]ast.Stmt{e})
						}
					})
				}
			case *ast.ForStmt:
				if s.Body != nil {
					simBranch(func() { walkStmts(s.Body.List) })
				}
			case *ast.RangeStmt:
				if s.Body != nil {
					simBranch(func() { walkStmts(s.Body.List) })
				}
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:

				switch e := st.(type) {
				case *ast.SwitchStmt:
					if e.Init != nil {
						walkStmts([]ast.Stmt{e.Init})
					}
					if call, ok := e.Tag.(*ast.CallExpr); ok {

						applyCall(call)
					}
					walkClauseBody(e.Body, !switchHasDefault(e.Body))
				case *ast.TypeSwitchStmt:
					if e.Init != nil {
						walkStmts([]ast.Stmt{e.Init})
					}
					walkClauseBody(e.Body, !switchHasDefault(e.Body))
				case *ast.SelectStmt:

					walkClauseBody(e.Body, false)
				}
			default:

				if s, ok := st.(*ast.GoStmt); ok {
					if fl, ok := s.Call.Fun.(*ast.FuncLit); ok {
						if fl.Body != nil {
							simBranch(func() { walkStmts(fl.Body.List) })
						}
					} else {
						applyCall(s.Call)
					}
					continue
				}

				_ = s
			}
		}
	}

	walkStmts(fn.Body.List)

	reportHeld("held at function end")

	return instances
}

func copySimHeld(held map[string]*simHeldEntry) map[string]*simHeldEntry {
	out := make(map[string]*simHeldEntry, len(held))
	for k, v := range held {
		cp := *v
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

type lockCall struct {
	receiver string
	method   string
	pos      token.Pos
}

func findLockCalls(fn *ast.FuncDecl) []lockCall {
	var locks []lockCall

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var call *ast.CallExpr

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
