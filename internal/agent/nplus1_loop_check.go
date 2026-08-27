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
//   - N=100 items x 10ms = 1 second of latency
//   - It exhausts connection pools and file descriptors
//   - It's the #1 performance complaint in production web apps
//
// Detection approach: AST-based analysis. Find for/range/for-condition
// loops, then scan their bodies for calls to known I/O functions:
//   - Database: db.Query, db.Exec, db.Get, db.Select, .Find, .First, .Where
//   - HTTP: http.Get, http.Post, client.Do, client.Get, client.Post
//   - File I/O: os.ReadFile, os.WriteFile, ioutil.ReadFile, os.Open
//   - Redis: rdb.Get, rdb.Set, rdb.HGet
// The check is delta-aware (only fires if the loop or I/O call is new).
//
// Fixes applied:
//   - #1135: method-suffix matching restricted. Precise SQL/HTTP method
//     names (Query/QueryRow/Exec/...Context) keep broad matching; generic
//     names (Get/Save/Delete/Do/Find/...) additionally require the receiver
//     identifier to carry a storage/HTTP signal, so pure in-memory calls
//     such as cache.Get or registry.Find no longer report false positives.
//   - #1136: delta keys are position-independent (function name plus a
//     normalized rendering of the call subtree), following the #1128 fix in
//     nil_deref_check.go; token.Pos is kept for display only.
//   - #1137: findIOInLoops traverses loops in a single pass and de-duplicates
//     visited call sites, so a nested-loop call is counted exactly once.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// ioExactPatterns maps fully qualified I/O function names to human-readable
// descriptions. These are matched exactly against the call expression's
// dotted name (e.g. "http.Get", "os.ReadFile"), independent of receiver
// heuristics (#1135).
var ioExactPatterns = map[string]string{
	// HTTP package-level helpers
	"http.Get":  "HTTP request",
	"http.Post": "HTTP request",
	"http.Head": "HTTP request",

	// File I/O
	"os.ReadFile":      "file read",
	"os.WriteFile":     "file write",
	"ioutil.ReadFile":  "file read",
	"ioutil.WriteFile": "file write",
	"os.Open":          "file open",
}

// ioPreciseMethodPatterns maps method suffixes that are precise enough to
// indicate database access on any receiver (#1135). These method names carry
// little ambiguity, so broad suffix matching is retained.
var ioPreciseMethodPatterns = map[string]string{
	".Query":           "database query",
	".QueryRow":        "database query",
	".QueryContext":    "database query",
	".QueryRowContext": "database query",
	".Exec":            "database exec",
	".ExecContext":     "database exec",
	".GetWithContext":  "HTTP request",
}

// ioGenericMethodPatterns maps common method suffixes that are ambiguous on
// their own (#1135). They only fire when the receiver identifier carries a
// storage/HTTP signal (see ioReceiverSignals), preventing false positives on
// pure in-memory calls like cache.Get or registry.Find.
var ioGenericMethodPatterns = map[string]string{
	".Get":    "database Get",
	".Select": "database Select",
	".Find":   "database Find",
	".First":  "database First",
	".Where":  "database Where (GORM)",
	".Create": "database Create (GORM)",
	".Update": "database Update (GORM)",
	".Delete": "database Delete (GORM)",
	".Save":   "database Save (GORM)",
	".Do":     "HTTP request (client.Do)",
	".HGet":   "Redis HGet",
	".HSet":   "Redis HSet",
	".HMGet":  "Redis HMGet",
	".HMSet":  "Redis HMSet",
}

// ioReceiverSignals are substrings that, when present in the lowercased
// receiver identifier, mark it as database/Redis/HTTP related (#1135).
var ioReceiverSignals = []string{
	"db", "sql", "gorm", "xorm", "mongo", "redis", "http", "conn", "coll",
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
	//
	// #1136: keys are position-independent (function name plus normalized
	// call text), so inserting lines above the code no longer makes old and
	// new keys diverge and re-warn. This follows the #1128/#1119 fix in
	// nil_deref_check.go. Matching is count-based: adding a second identical
	// call still produces exactly one new warning.
	if oldAST != nil {
		budget := make(map[string]int)
		for _, p := range newPatterns {
			budget[p.String()]++
		}
		for _, p := range findIOInLoops(oldAST) {
			if c := budget[p.String()]; c > 0 {
				budget[p.String()] = c - 1
			}
		}
		var delta []ioLoopPattern
		for _, p := range newPatterns {
			if budget[p.String()] > 0 {
				budget[p.String()]--
				delta = append(delta, p)
			}
		}
		newPatterns = delta
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
	pos    token.Pos // display only (#1136); excluded from identity
	ioType string
	key    string // position-independent identity (#1136)
}

// String returns the delta key for this pattern. Position-independent by
// construction (#1136).
func (p ioLoopPattern) String() string {
	return p.key
}

// loopSite pairs a loop body with the name of the enclosing function, used to
// build position-independent delta keys (#1136).
type loopSite struct {
	body   *ast.BlockStmt
	fnName string
}

// findIOInLoops walks the AST in a single pass and finds I/O calls inside
// for/range loops.
//
// #1137: the previous implementation used two Inspect sweeps (one per loop
// form) plus a nested Inspect over every loop body encountered during the
// outer sweep, so a call nested K levels deep was visited up to K times and
// duplicated into the warning budget. Here loops are collected once and each
// call site is de-duplicated via a visited set keyed by the call's AST node,
// guaranteeing exactly one report per distinct I/O call site.
func findIOInLoops(file *ast.File) []ioLoopPattern {
	var sites []loopSite

	// Single sweep: collect every loop root (nested loops are collected when
	// reached; each root is scanned exactly once below).
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ForStmt:
			if v.Body != nil {
				sites = append(sites, loopSite{body: v.Body})
			}
		case *ast.RangeStmt:
			if v.Body != nil {
				sites = append(sites, loopSite{body: v.Body})
			}
		}
		return true
	})

	patterns := make([]ioLoopPattern, 0, len(sites))
	visited := make(map[*ast.CallExpr]bool) // #1137: one report per call site

	for _, site := range sites {
		site.fnName = ioEnclosingFuncName(file, site.body.Pos())
		ast.Inspect(site.body, func(inner ast.Node) bool {
			// Don't descend into nested function literals - their loops
			// are separate scopes and the I/O may be properly batched
			// via goroutines/channels.
			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if visited[call] {
				return true
			}
			visited[call] = true

			ioType := identifyIOCall(call)
			if ioType != "" {
				patterns = append(patterns, ioLoopPattern{
					pos:    call.Pos(),
					ioType: ioType,
					key: fmt.Sprintf("%s|%s|%s", ioType, site.fnName,
						ioNormalizeCallText(call)),
				})
			}
			return true
		})
	}

	return patterns
}

// ioEnclosingFuncName returns the name of the innermost FuncDecl containing
// pos, or "_" when the position is not inside a named function.
func ioEnclosingFuncName(file *ast.File, pos token.Pos) string {
	name := "_"
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Pos() <= pos && pos <= fn.End() {
			name = fn.Name.Name
			break
		}
	}
	return name
}

// identifyIOCall checks if a call expression matches a known I/O pattern.
// Returns a description string if matched, "" otherwise.
//
// #1135: matching is tiered.
//   - Exact dotted names (http.Get, os.ReadFile) match as before.
//   - Precise SQL/HTTP method suffixes (Query/QueryRow/Exec/...Context)
//     keep broad receiver-independent matching.
//   - Generic method suffixes (Get/Save/Delete/Do/Find/...) require the
//     receiver identifier to carry a storage/HTTP signal, so pure in-memory
//     receivers such as cache, lru or registry no longer trigger warnings.
func identifyIOCall(call *ast.CallExpr) string {
	name := callFuncName(call)
	if name == "" {
		return ""
	}

	// Exact matches first (e.g. "http.Get", "os.ReadFile").
	if desc, ok := ioExactPatterns[name]; ok {
		return desc
	}

	if _, isSelector := call.Fun.(*ast.SelectorExpr); !isSelector {
		return ""
	}

	// Precise method names: unambiguous SQL/HTTP verbs.
	for suffix, desc := range ioPreciseMethodPatterns {
		if strings.HasSuffix(name, suffix) {
			return desc
		}
	}

	// Generic method names: require a storage/HTTP receiver signal (#1135).
	for suffix, desc := range ioGenericMethodPatterns {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		if ioHasReceiverSignal(call.Fun.(*ast.SelectorExpr).X) {
			return desc
		}
	}

	return ""
}

// ioHasReceiverSignal reports whether the base identifier of the receiver
// expression carries a database/Redis/HTTP signal (#1135). A bare receiver
// such as cache, lru or registry yields false, silencing in-memory FP calls.
func ioHasReceiverSignal(recv ast.Expr) bool {
	name := ioReceiverIdentifier(recv)
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, sig := range ioReceiverSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// ioReceiverIdentifier extracts a meaningful identifier from a receiver
// expression for signal matching (#1135). Plain identifiers are returned as
// is; selector receivers fall back to their field name (e.g. cfg.MyDB gives
// "MyDB"), parenthesized/pointer expressions unwrap.
func ioReceiverIdentifier(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.ParenExpr:
		return ioReceiverIdentifier(v.X)
	case *ast.StarExpr:
		return ioReceiverIdentifier(v.X)
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

// callFuncName returns the dotted function name of a call expression,
// e.g. "db.Query", "http.Get", "myclient.Post". Returns "" when the callee
// is not a plain identifier or selector chain.
func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		parts := make([]string, 0, 4)
		ioAppendSelectorParts(&parts, fn)
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

// ioAppendSelectorParts flattens a selector chain into dot-joined parts.
func ioAppendSelectorParts(parts *[]string, expr *ast.SelectorExpr) {
	switch x := expr.X.(type) {
	case *ast.Ident:
		*parts = append(*parts, x.Name)
	case *ast.SelectorExpr:
		ioAppendSelectorParts(parts, x)
	}
	*parts = append(*parts, expr.Sel.Name)
}

// ioNormalizeCallText renders a call expression as position-independent text
// (#1136). Byte offsets, formatting width and neighboring comments are all
// ignored - the same call keeps the same key regardless of where lines move.
func ioNormalizeCallText(call *ast.CallExpr) string {
	var b strings.Builder
	ioAppendNormalized(&b, call)
	return b.String()
}

// ioAppendNormalized writes a canonical structural rendering of the AST node.
func ioAppendNormalized(b *strings.Builder, n ast.Node) {
	switch v := n.(type) {
	case *ast.Ident:
		b.WriteString(v.Name)
	case *ast.BasicLit:
		b.WriteString(v.Kind.String())
		b.WriteByte(':')
		b.WriteString(v.Value)
	case *ast.SelectorExpr:
		ioAppendNormalized(b, v.X)
		b.WriteByte('.')
		b.WriteString(v.Sel.Name)
	case *ast.ParenExpr:
		b.WriteByte('(')
		ioAppendNormalized(b, v.X)
		b.WriteByte(')')
	case *ast.StarExpr:
		b.WriteByte('*')
		ioAppendNormalized(b, v.X)
	case *ast.UnaryExpr:
		b.WriteString(v.Op.String())
		ioAppendNormalized(b, v.X)
	case *ast.BinaryExpr:
		ioAppendNormalized(b, v.X)
		b.WriteString(v.Op.String())
		ioAppendNormalized(b, v.Y)
	case *ast.IndexExpr:
		ioAppendNormalized(b, v.X)
		b.WriteByte('[')
		ioAppendNormalized(b, v.Index)
		b.WriteByte(']')
	case *ast.KeyValueExpr:
		ioAppendNormalized(b, v.Key)
		b.WriteByte(':')
		ioAppendNormalized(b, v.Value)
	case *ast.Ellipsis:
		b.WriteString("...")
		if v.Elt != nil {
			ioAppendNormalized(b, v.Elt)
		}
	case *ast.CompositeLit:
		b.WriteString("composite{}")
	case *ast.CallExpr:
		ioAppendNormalized(b, v.Fun)
		b.WriteByte('(')
		for i, arg := range v.Args {
			if i > 0 {
				b.WriteByte(',')
			}
			ioAppendNormalized(b, arg)
		}
		if v.Ellipsis != token.NoPos {
			b.WriteString("...")
		}
		b.WriteByte(')')
	default:
		fmt.Fprintf(b, "<%T>", n)
	}
}
