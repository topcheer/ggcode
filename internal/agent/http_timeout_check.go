package agent

// HTTP Client Missing-Timeout Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that makes HTTP
// requests without any timeout. The standard library's http.Get, http.Post,
// http.Head, and http.PostForm all use http.DefaultClient, which has NO
// timeout configured. Similarly, &http.Client{} created without a Timeout
// field also has no timeout. In production, this causes goroutine leaks,
// connection pool exhaustion, and cascading failures when a downstream
// service becomes unresponsive.
//
// This is a DIFFERENT class of bug from resource leaks (missing Close()):
//   - Resource leak: resp.Body is never closed -> file descriptor exhaustion
//   - Missing timeout: the request never returns -> goroutine/connection leak
//
// A function can pass the resource-leak check (has defer resp.Body.Close())
// but still hang indefinitely because there is no timeout. Both checks are
// needed.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via golangci-lint)
//   - Cline/OpenHands: reactive only -- caught by production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - staticcheck (S1011): does not flag missing timeouts
//   - gosec (G107/G112): flags some SSRF patterns but not missing timeouts
//
// None provide INLINE detection at write time. This check provides immediate,
// zero-dependency feedback in <1ms per file using Go's standard library AST
// parser.
//
// Approach: AST-based analysis of Go source. Detects three patterns:
//  1. http.Get/Post/Head/PostForm -- always use DefaultClient (no timeout)
//  2. http.DefaultClient.Do/Get/Post -- explicit no-timeout client usage
//  3. &http.Client{...} without a Timeout field -- custom client missing timeout
//
// The check is delta-aware: only patterns newly introduced by this edit are
// reported, avoiding re-reporting pre-existing issues on every subsequent edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// noTimeoutHTTPFuncs lists http package-level functions that use DefaultClient
// (which has no timeout).
var noTimeoutHTTPFuncs = map[string]bool{
	"Get":      true,
	"Post":     true,
	"Head":     true,
	"PostForm": true,
}

// checkHTTPT imeout detects HTTP client usage patterns that lack timeout
// configuration. Returns warnings for calls that can hang indefinitely.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkHTTPTimeout(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Parse the new content.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil // syntax errors are reported by other checks
	}

	newIssues := findHTTPTimeoutIssues(file, fset)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: parse old content and subtract pre-existing issues.
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil && oldFile != nil {
			oldSet := issueSet(findHTTPTimeoutIssues(oldFile, oldFset))
			filtered := newIssues[:0]
			for _, iss := range newIssues {
				if !oldSet[iss.key] {
					filtered = append(filtered, iss)
				}
			}
			newIssues = filtered
		}
	}

	if len(newIssues) == 0 {
		return nil
	}

	var warnings []string
	for _, iss := range newIssues {
		warnings = append(warnings, iss.message)
	}
	return warnings
}

// timeoutIssue represents a single detected missing-timeout pattern.
type timeoutIssue struct {
	key     string // deduplication key (pattern + message type)
	message string // human-readable warning
}

// httpTimeoutFinding holds details about a detected pattern for delta comparison.
type httpTimeoutFinding struct {
	pos     token.Pos
	pattern string // "http.Get", "http.DefaultClient.Do", "http.Client{}"
	kind    string // "default-client-func", "default-client-explicit", "custom-client-no-timeout"
}

// findHTTPTimeoutIssues walks the AST and collects all missing-timeout patterns.
func findHTTPTimeoutIssues(file *ast.File, fset *token.FileSet) []timeoutIssue {
	var findings []httpTimeoutFinding

	// Track which http.Client variables/composites have a Timeout field.
	clientsWithTimeout := make(map[*ast.CompositeLit]bool)
	// Track all http.Client composite literals.
	allClientComposites := make(map[*ast.CompositeLit]bool)

	ast.Inspect(file, func(node ast.Node) bool {
		// Detect &http.Client{...} composite literals.
		if comp := findHTTPClientComposite(node); comp != nil {
			allClientComposites[comp] = true
			if hasTimeoutField(comp) {
				clientsWithTimeout[comp] = true
			}
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Pattern 1: http.Get/Post/Head/PostForm
		if finding := matchNoTimeoutHTTPFunc(call); finding != nil {
			findings = append(findings, *finding)
			return true
		}

		// Pattern 2: http.DefaultClient.Do/Get/Post
		if finding := matchDefaultClientCall(call); finding != nil {
			findings = append(findings, *finding)
			return true
		}

		return true
	})

	// Pattern 3: http.Client composite literals without Timeout field.
	for comp := range allClientComposites {
		if !clientsWithTimeout[comp] {
			findings = append(findings, httpTimeoutFinding{
				pos:     comp.Pos(),
				pattern: "http.Client{}",
				kind:    "custom-client-no-timeout",
			})
		}
	}

	// Convert findings to timeoutIssues with messages.
	var issues []timeoutIssue
	for _, f := range findings {
		posStr := fset.Position(f.pos).String()
		switch f.kind {
		case "default-client-func":
			issues = append(issues, timeoutIssue{
				key: f.kind + ":" + f.pattern,
				message: fmt.Sprintf(
					"%s() at %s uses http.DefaultClient which has NO timeout -- the request can hang "+
						"indefinitely, causing goroutine/connection leaks and cascading failures. "+
						"Use a custom client with a Timeout (e.g., &http.Client{Timeout: 30 * time.Second}) "+
						"or use http.NewRequestWithContext with a context bearing a deadline.",
					f.pattern, posStr),
			})
		case "default-client-explicit":
			issues = append(issues, timeoutIssue{
				key: f.kind + ":" + f.pattern,
				message: fmt.Sprintf(
					"http.DefaultClient at %s has NO timeout -- the request can hang indefinitely. "+
						"Use a custom http.Client with a Timeout field or use "+
						"http.NewRequestWithContext with a deadline-bearing context.",
					posStr),
			})
		case "custom-client-no-timeout":
			issues = append(issues, timeoutIssue{
				key: f.kind + ":" + f.pattern,
				message: fmt.Sprintf(
					"&http.Client{} at %s is created without a Timeout field -- requests made with "+
						"this client can hang indefinitely. Add a Timeout field "+
						"(e.g., Timeout: 30 * time.Second) or use context-based deadlines.",
					posStr),
			})
		}
	}

	return issues
}

// findHTTPClientComposite checks if a node is an &http.Client{...} composite
// literal (possibly wrapped in parentheses). Returns the CompositeLit or nil.
func findHTTPClientComposite(node ast.Node) *ast.CompositeLit {
	// Direct CompositeLit.
	if comp, ok := node.(*ast.CompositeLit); ok {
		if isHTTPClientType(comp.Type) {
			return comp
		}
	}
	// &CompositeLit (UnaryExpr).
	if unary, ok := node.(*ast.UnaryExpr); ok {
		if comp, ok := unary.X.(*ast.CompositeLit); ok {
			if isHTTPClientType(comp.Type) {
				return comp
			}
		}
	}
	// ParenExpr wrapping.
	if paren, ok := node.(*ast.ParenExpr); ok {
		return findHTTPClientComposite(paren.X)
	}
	return nil
}

// isHTTPClientType returns true if the AST expression represents http.Client.
func isHTTPClientType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "http" && sel.Sel.Name == "Client"
}

// hasTimeoutField checks if an http.Client composite literal includes a
// Timeout key-value element.
func hasTimeoutField(comp *ast.CompositeLit) bool {
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if ident.Name == "Timeout" {
			return true
		}
	}
	return false
}

// matchNoTimeoutHTTPFunc checks if a call is http.Get/Post/Head/PostForm.
func matchNoTimeoutHTTPFunc(call *ast.CallExpr) *httpTimeoutFinding {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if pkg.Name != "http" {
		return nil
	}
	if !noTimeoutHTTPFuncs[sel.Sel.Name] {
		return nil
	}
	return &httpTimeoutFinding{
		pos:     call.Pos(),
		pattern: "http." + sel.Sel.Name,
		kind:    "default-client-func",
	}
}

// matchDefaultClientCall checks if a call uses http.DefaultClient explicitly.
func matchDefaultClientCall(call *ast.CallExpr) *httpTimeoutFinding {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	// The receiver should be http.DefaultClient (a SelectorExpr).
	recv, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkg, ok := recv.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if pkg.Name == "http" && recv.Sel.Name == "DefaultClient" {
		return &httpTimeoutFinding{
			pos:     call.Pos(),
			pattern: "http.DefaultClient." + sel.Sel.Name,
			kind:    "default-client-explicit",
		}
	}
	return nil
}

// issueSet converts a slice of timeoutIssues to a set keyed by issue.key.
func issueSet(issues []timeoutIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
