package agent

// Concurrent Map Access Detection in Go Code
//
// Problem: AI coding agents frequently generate Go code that accesses maps
// from multiple goroutines without proper synchronization. Go's runtime
// detects this at runtime with a fatal crash:
//
//	fatal error: concurrent map writes
//	fatal error: concurrent map read and map write
//
// These crashes are non-recoverable (they abort the entire process) and are
// among the most common production incidents in Go services. Unlike data
// races (which require -race flag to detect), concurrent map access crashes
// happen unconditionally whenever the timing aligns.
//
// Common LLM failure modes this check catches:
//  1. Map declared as struct field, written in one goroutine, read in another
//     without a mutex: `go s.process()` where process reads/writes s.items
//  2. Map accessed inside a goroutine spawned by a method that also modifies
//     the same map: `go func() { m[key] = val }()` without sync
//  3. Map passed to a goroutine and concurrently modified by the caller
//  4. Map used as a cache shared across goroutines with no synchronization
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external -race testing)
//   - Cursor: go vet does NOT catch this (it only checks for some race patterns)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: no automatic detection
//
// go vet does NOT detect concurrent map access. The -race detector catches
// it at runtime but only if the concurrent path is actually exercised during
// testing. staticcheck does not check this either.
//
// Approach: Heuristic AST-based analysis. For each function, if it (a) spawns
// goroutines via `go` statements AND (b) has map read/write operations on the
// same map variable, AND (c) does NOT use sync primitives (Mutex, RWMutex,
// sync.Map) to protect those accesses, flag as a potential concurrent map
// access. This is conservative: it only flags within a single function scope
// where both the goroutine spawn and map access coexist without sync.
//
// False positive mitigation:
//   - Channels: if the map is only accessed through channel-mediated patterns,
//     we skip (hard to detect precisely, but we check for obvious sync usage)
//   - sync.Map: explicitly excluded (designed for concurrent use)
//   - Functions with Mutex/RWMutex/Locker calls are excluded
//   - Delta-aware: only flags patterns newly introduced by this edit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// concurrentMapInstance represents a potential concurrent map access.
type concurrentMapInstance struct {
	posStr   string // human-readable position
	mapName  string // the map variable name
	goCalled bool   // whether this is in a goroutine context
}

// checkConcurrentMapAccess detects potential concurrent map access in Go code
// where maps are accessed both directly and inside spawned goroutines without
// synchronization. Delta-aware: only flags NEW instances.
func checkConcurrentMapAccess(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return ""
	}

	instances := findConcurrentMapAccess(fset, file)
	if len(instances) == 0 {
		return ""
	}

	// Delta check: count patterns in old content.
	oldCount := countConcurrentMapIssues(oldContent)
	if len(instances) <= oldCount {
		return ""
	}

	// Only flag newly introduced instances.
	newCount := len(instances) - oldCount
	var b strings.Builder
	b.WriteString("[Concurrent map access detection] Potential unsynchronized concurrent map access detected.\n")
	b.WriteString("Go maps are NOT safe for concurrent use - the runtime will fatally crash with 'concurrent map read/write'.\n")
	for i := 0; i < newCount && i+oldCount < len(instances); i++ {
		inst := instances[oldCount+i]
		b.WriteString(fmt.Sprintf("  - %s: map '%s' is accessed in a function that spawns goroutines without sync (Mutex/RWMutex/sync.Map). ",
			inst.posStr, inst.mapName))
		b.WriteString("Protect with sync.RWMutex, or use sync.Map for concurrent access patterns.\n")
	}
	return b.String()
}

// countConcurrentMapIssues counts concurrent map patterns in source text.
func countConcurrentMapIssues(src string) int {
	if strings.TrimSpace(src) == "" {
		return 0
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return 0
	}
	return len(findConcurrentMapAccess(fset, file))
}

// findConcurrentMapAccess performs heuristic detection of potential concurrent
// map access within each function. A function is flagged if:
//  1. It contains at least one `go` statement (goroutine spawn)
//  2. It has map write operations (m[k] = v, delete(m, k))
//  3. It does NOT declare or use sync primitives (Mutex, RWMutex, sync.Map)
//
// We focus on map WRITES because those are the ones that cause fatal crashes.
// Map reads combined with concurrent writes also crash, but if we detect the
// write pattern we already flag it.
func findConcurrentMapAccess(fset *token.FileSet, file *ast.File) []concurrentMapInstance {
	var instances []concurrentMapInstance

	for _, topDecl := range file.Decls {
		fn, ok := topDecl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Collect function-level info.
		info := analyzeFuncForMapConcurrency(fn.Body)
		if len(info.unsyncMapWrites) == 0 || !info.hasGoStatement {
			continue
		}

		// Flag each unsynchronized map write.
		for mapName, pos := range info.unsyncMapWrites {
			p := fset.Position(pos)
			instances = append(instances, concurrentMapInstance{
				posStr:   fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line),
				mapName:  mapName,
				goCalled: true,
			})
		}
	}

	return instances
}

// mapConcurrencyInfo holds analysis results for a function body.
type mapConcurrencyInfo struct {
	hasGoStatement  bool
	hasSync         bool                 // uses Mutex, RWMutex, sync.Map, or Locker
	unsyncMapWrites map[string]token.Pos // map var name -> position of first write
}

// analyzeFuncForMapConcurrency inspects a function body for concurrent map
// access patterns.
func analyzeFuncForMapConcurrency(body *ast.BlockStmt) mapConcurrencyInfo {
	info := mapConcurrencyInfo{
		unsyncMapWrites: make(map[string]token.Pos),
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			info.hasGoStatement = true

		case *ast.SelectorExpr:
			// Detect sync.Map usage: sync.Map or field of type accessed via
			// sync.Map methods (Store, Load, LoadOrStore, Delete, Range).
			if ident, ok := node.X.(*ast.Ident); ok {
				if ident.Name == "sync" && node.Sel != nil {
					switch node.Sel.Name {
					case "Map", "Mutex", "RWMutex", "WaitGroup", "Once", "Pool", "Locker":
						info.hasSync = true
					}
				}
			}

		case *ast.CallExpr:
			// Detect Lock/Unlock/RLock/RUnlock method calls as evidence of sync.
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil {
				switch sel.Sel.Name {
				case "Lock", "Unlock", "RLock", "RUnlock", "TryLock", "TryRLock":
					info.hasSync = true
				}
			}

			// Detect mutex type declarations via make or struct literal.
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "Lock" {
				info.hasSync = true
			}

			// Detect delete(m, k) -- another map write operation.
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "delete" && len(node.Args) > 0 {
				if mapName := mapVarName(node.Args[0]); mapName != "" {
					if _, exists := info.unsyncMapWrites[mapName]; !exists {
						info.unsyncMapWrites[mapName] = node.Pos()
					}
				}
			}

		case *ast.AssignStmt:
			// Detect map write: m[k] = v
			if node.Tok == token.ASSIGN || node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if idx, ok := lhs.(*ast.IndexExpr); ok {
						if mapName := mapVarName(idx.X); mapName != "" {
							if _, exists := info.unsyncMapWrites[mapName]; !exists {
								info.unsyncMapWrites[mapName] = node.Pos()
							}
						}
					}
				}
			}
		}
		return true
	})

	// If the function uses sync primitives, clear the warnings.
	if info.hasSync {
		info.unsyncMapWrites = make(map[string]token.Pos)
	}

	return info
}

// mapVarName extracts the variable name from a map expression, handling
// simple identifiers (m), selector expressions (s.items), and returning
// empty string for complex expressions.
func mapVarName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if base, ok := e.X.(*ast.Ident); ok {
			return base.Name + "." + e.Sel.Name
		}
		return ""
	default:
		return ""
	}
}
