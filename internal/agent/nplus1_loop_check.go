package agent

// Post-Edit N+1 I/O in Loop Detection (Check #48)
//
// Trend: AI Agent Performance Awareness - N+1 Query Pattern Detection
//
// Problem: AI coding agents frequently generate code that performs I/O
// operations (database queries, HTTP requests, file reads/writes) inside
// for/range loops. This is the classic "N+1 query" anti-pattern - instead
// of batching operations, each iteration triggers a separate I/O call,
// causing O(N) network/disk round-trips. For N=1000 items, this means
// 1000 database queries or HTTP requests instead of 1 batch query.
//
// This is distinct from check #36 (loop_perf_check) which only catches
// O(n^2) string concatenation. N+1 I/O in loops is arguably MORE impactful
// because:
//   - Each I/O call has network/disk latency (1-100ms per call)
//   - N=100 items × 10ms = 1 second of latency
//   - It exhausts connection pools and file descriptors
//   - It's the #1 performance complaint in production web apps
//
// Competitor analysis:
//   - Claude Code: no detection (relies on external profilers)
//   - Cursor: may catch via language server diagnostics, but not N+1 patterns
//   - Cline/OpenHands: reactive only - caught by load testing
//   - Aider: no detection
//   - go vet: does not flag I/O in loops
//   - staticcheck: does not flag I/O in loops
//   - gocritic: no N+1 detection
//
// Detection approach: AST-based analysis. Find for/range/for-condition
// loops, then scan their bodies for calls to known I/O functions:
//   - Database: db.Query, db.Exec, db.Get, db.Select, .Find, .First, .Where
//   - HTTP: http.Get, http.Post, client.Do, client.Get, client.Post
//   - File I/O: os.ReadFile, os.WriteFile, ioutil.ReadFile, os.Open
//   - Redis: rdb.Get, rdb.Set, rdb.HGet
// The check is delta-aware (only fires if the loop or I/O call is new).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// ioCallPatterns maps I/O function patterns to human-readable descriptions.
// These are matched against the call expression's function name.
var ioCallPatterns = map[string]string{
	// Database operations (GORM, sqlx, database/sql)
	".Query":    "database query",
	".QueryRow": "database query",
	".Exec":     "database exec",
	".Get":      "database Get",
	".Select":   "database Select",
	".Find":     "database Find",
	".First":    "database First",
	".Where":    "database Where (GORM)",
	".Create":   "database Create (GORM)",
	".Update":   "database Update (GORM)",
	".Delete":   "database Delete (GORM)",
	".Save":     "database Save (GORM)",

	// HTTP client operations
	"http.Get":  "HTTP request",
	"http.Post": "HTTP request",
	"http.Head": "HTTP request",
	".Do":       "HTTP request (client.Do)",

	// File I/O
	"os.ReadFile":      "file read",
	"os.WriteFile":     "file write",
	"ioutil.ReadFile":  "file read",
	"ioutil.WriteFile": "file write",
	"os.Open":          "file open",

	// Redis operations
	".HGet":  "Redis HGet",
	".HSet":  "Redis HSet",
	".HMGet": "Redis HMGet",
	".HMSet": "Redis HMSet",
}

// checkNPlus1Loop detects I/O operations inside for/range loops, which is
// the N+1 query anti-pattern. Delta-aware: only fires if the pattern is
// newly introduced by this edit.
func checkNPlus1Loop(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Parse the new content.
	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil // syntax errors are handled by other checks
	}

	// Parse old content to determine delta.
	var oldAST *ast.File
	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ = parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
	}

	// Find I/O-in-loop patterns in new content.
	newPatterns := findIOInLoops(newAST)
	if len(newPatterns) == 0 {
		return nil
	}

	// Delta: subtract patterns already present in old content.
	if oldAST != nil {
		oldPatterns := findIOInLoops(oldAST)
		if len(oldPatterns) > 0 {
			oldSet := make(map[string]bool)
			for _, p := range oldPatterns {
				oldSet[p.String()] = true
			}
			var delta []ioLoopPattern
			for _, p := range newPatterns {
				if !oldSet[p.String()] {
					delta = append(delta, p)
				}
			}
			newPatterns = delta
		}
	}

	if len(newPatterns) == 0 {
		return nil
	}

	// Build warning.
	var warnings []string
	for i, p := range newPatterns {
		if i >= 2 { // cap at 2 to avoid flooding
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"N+1 I/O anti-pattern: %s call inside loop at %s:%d. "+
				"Each iteration triggers a separate I/O round-trip (N iterations = N calls). "+
				"Consider batching: use a single query with WHERE IN, bulk insert, or collect IDs first then fetch in one call.",
			p.ioType, filepath.Base(filePath), fset.Position(p.pos).Line))
	}

	if extra := len(newPatterns) - 2; extra > 0 {
		warnings = append(warnings, fmt.Sprintf("...and %d more N+1 I/O pattern(s) in %s", extra, filepath.Base(filePath)))
	}

	return warnings
}

// ioLoopPattern represents a detected I/O call inside a loop.
type ioLoopPattern struct {
	pos    token.Pos
	ioType string
}

func (p ioLoopPattern) String() string {
	return fmt.Sprintf("%s@%d", p.ioType, p.pos)
}

// findIOInLoops walks the AST and finds I/O calls inside for/range loops.
func findIOInLoops(file *ast.File) []ioLoopPattern {
	var patterns []ioLoopPattern

	ast.Inspect(file, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok {
			return true
		}

		// Walk the body of this for loop looking for I/O calls.
		if forStmt.Body == nil {
			return true
		}

		ast.Inspect(forStmt.Body, func(inner ast.Node) bool {
			// Don't descend into nested function literals — their loops
			// are separate scopes and the I/O may be properly batched
			// via goroutines/channels.
			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}

			ioType := identifyIOCall(call)
			if ioType != "" {
				patterns = append(patterns, ioLoopPattern{
					pos:    call.Pos(),
					ioType: ioType,
				})
			}
			return true
		})

		return true
	})

	// Also check range statements (which are ForStmt with Range == true in some
	// cases, or represented differently). In Go AST, range loops are *ast.RangeStmt.
	ast.Inspect(file, func(n ast.Node) bool {
		rangeStmt, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}

		if rangeStmt.Body == nil {
			return true
		}

		ast.Inspect(rangeStmt.Body, func(inner ast.Node) bool {
			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}

			ioType := identifyIOCall(call)
			if ioType != "" {
				patterns = append(patterns, ioLoopPattern{
					pos:    call.Pos(),
					ioType: ioType,
				})
			}
			return true
		})

		return true
	})

	return patterns
}

// identifyIOCall checks if a call expression matches a known I/O pattern.
// Returns a description string if matched, "" otherwise.
func identifyIOCall(call *ast.CallExpr) string {
	name := callFuncName(call)
	if name == "" {
		return ""
	}

	// Check exact matches first (e.g., "http.Get", "os.ReadFile").
	if desc, ok := ioCallPatterns[name]; ok {
		return desc
	}

	// Check suffix matches for method calls (e.g., ".Query" matches
	// "db.Query", "tx.Query", "repo.Query").
	for suffix, desc := range ioCallPatterns {
		if strings.HasPrefix(suffix, ".") && strings.HasSuffix(name, suffix) {
			return desc
		}
	}

	return ""
}

// callFuncName extracts a readable name from a call expression's Fun field.
// For selector expressions (x.Y), returns "x.Y". For simple identifiers,
// returns the name. For more complex expressions, returns "".
func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		// Try to get the receiver as a string.
		var receiver string
		switch x := fn.X.(type) {
		case *ast.Ident:
			receiver = x.Name
		case *ast.SelectorExpr:
			// Package.Qualified.Type (e.g., http.Client.Get)
			receiver = selectorString(x)
		default:
			// Complex receiver — just use the field name.
			return "." + fn.Sel.Name
		}
		if receiver != "" {
			return receiver + "." + fn.Sel.Name
		}
		return "." + fn.Sel.Name
	default:
		return ""
	}
}

// selectorString converts a selector expression to a dotted string.
func selectorString(sel *ast.SelectorExpr) string {
	var parts []string
	var current ast.Node = sel
	for {
		s, ok := current.(*ast.SelectorExpr)
		if !ok {
			break
		}
		parts = append([]string{s.Sel.Name}, parts...)
		current = s.X
	}
	if ident, ok := current.(*ast.Ident); ok {
		parts = append([]string{ident.Name}, parts...)
	}
	return strings.Join(parts, ".")
}
