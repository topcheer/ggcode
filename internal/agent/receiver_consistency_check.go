package agent

// Inconsistent Receiver Name Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code where methods on the
// same receiver type use inconsistent receiver variable names. For example:
//
//	func (s *Server) Start() { ... }
//	func (srv *Server) Stop() { ... }
//	func (this *Server) Status() { ... }
//
// Go's compiler accepts this without error, but it violates the Go style
// guide ("Effective Go", "Code Review Comments") which states:
//   - Receiver names should be consistent across all methods of a type
//   - Receiver names should be short (1-2 characters)
//   - The name "this" and "self" are explicitly discouraged in Go
//
// Why this matters:
//   1. Code readability: inconsistent receivers make the codebase harder to
//      follow -- the reader can't quickly identify which type a method belongs to.
//   2. Lint failures: staticcheck (ST1016), golint, and golangci-lint all flag
//      this as a code quality issue.
//   3. Code review churn: reviewers flag this as a "must-fix" before approval.
//   4. Copy-paste bugs: when the agent copies a method from one file and adapts
//      it in another, the receiver name often diverges -- a sign of careless
//      generation that may hide deeper issues.
//
// Common LLM failure modes this check catches:
//   1. Different short names: (t *Tree) in one method, (n *Tree) in another
//   2. "this"/"self" anti-pattern: common when the model has seen Java/Python
//      code and applies the convention to Go
//   3. Abbreviation drift: (cfg *Config) vs (c *Config) across methods
//   4. Generated method with wrong receiver: agent adds a method to type Foo
//      but uses the receiver name from type Bar
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: detects via gopls/golangci-lint integration, but not inline
//   - Cline/OpenHands: reactive only -- caught at lint or review time
//   - Aider: no automatic detection
//   - staticcheck ST1016: catches this but requires running an external tool
//
// Approach: AST-based analysis. For each .go file, collect all method
// receivers grouped by their type name. If a type has multiple distinct
// receiver names, flag the inconsistency. Delta-aware: only flags if the
// inconsistency was introduced or worsened by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// receiverAntiPatternNames are explicitly discouraged receiver names.
var receiverAntiPatternNames = map[string]bool{
	"this": true,
	"self": true,
}

// receiverInstance represents a single method's receiver declaration.
type receiverInstance struct {
	typeName string // the Go type name (e.g., "Server")
	varName  string // the receiver variable name (e.g., "s")
	posStr   string // human-readable position
}

// inconsistentReceiverGroup represents a type with multiple receiver names.
type inconsistentReceiverGroup struct {
	typeName string
	names    map[string][]receiverInstance // varName → instances
}

// checkReceiverConsistency performs AST-based receiver name consistency analysis.
// Returns warnings for types where methods use inconsistent receiver names.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkReceiverConsistency(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Parse the new content.
	newGroups := findReceiverInconsistencies(newContent)
	if len(newGroups) == 0 {
		return nil
	}

	// Delta-aware: only flag if the inconsistency is new or worsened.
	oldGroups := findReceiverInconsistencies(oldContent)
	oldInconsistentTypes := make(map[string]bool)
	for _, g := range oldGroups {
		if len(g.names) > 1 {
			oldInconsistentTypes[g.typeName] = true
		}
	}

	var warnings []string
	for _, g := range newGroups {
		if len(g.names) <= 1 {
			continue
		}
		// Skip if this type was already inconsistent before the edit.
		if oldInconsistentTypes[g.typeName] {
			continue
		}

		// Build the list of distinct names with their positions.
		var nameList []string
		var positions []string
		for name, instances := range g.names {
			marker := name
			if receiverAntiPatternNames[name] {
				marker = fmt.Sprintf("%s (anti-pattern: use a short type-derived name instead)", name)
			}
			nameList = append(nameList, marker)
			if len(instances) > 0 {
				positions = append(positions, fmt.Sprintf("  %s: %s", name, instances[0].posStr))
			}
		}

		warnings = append(warnings, fmt.Sprintf(
			"Inconsistent receiver names on type `%s`: methods use different receiver names (%s). "+
				"Go style requires all methods on a type to use the same receiver name. "+
				"Pick one consistent short name (typically 1-2 chars derived from the type name).\n%s",
			g.typeName, strings.Join(nameList, ", "),
			strings.Join(positions, "\n")))
	}

	return warnings
}

// findReceiverInconsistencies parses Go source and groups method receivers by
// type name. Returns groups for ALL types (even consistent ones) so callers
// can diff old vs new.
func findReceiverInconsistencies(src string) []inconsistentReceiverGroup {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Collect receiver instances per type.
	typeKey := map[string]*inconsistentReceiverGroup{}
	var orderedTypes []string // preserves first-seen order

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}

		// Extract receiver type name (unwrap pointer).
		recvType := extractRecvTypeName(fn.Recv.List[0].Type)
		if recvType == "" {
			continue
		}

		// Extract receiver variable name.
		varName := ""
		if len(fn.Recv.List[0].Names) > 0 {
			varName = fn.Recv.List[0].Names[0].Name
		}
		if varName == "" || varName == "_" {
			continue // blank receiver, skip
		}

		pos := fset.Position(fn.Pos())

		g, exists := typeKey[recvType]
		if !exists {
			g = &inconsistentReceiverGroup{
				typeName: recvType,
				names:    make(map[string][]receiverInstance),
			}
			typeKey[recvType] = g
			orderedTypes = append(orderedTypes, recvType)
		}

		inst := receiverInstance{
			typeName: recvType,
			varName:  varName,
			posStr:   fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
		}
		g.names[varName] = append(g.names[varName], inst)
	}

	var result []inconsistentReceiverGroup
	for _, t := range orderedTypes {
		result = append(result, *typeKey[t])
	}
	return result
}

// extractRecvTypeName extracts the type name from a receiver type expression,
// unwrapping pointer and generic wrappers.
func extractRecvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return extractRecvTypeName(e.X)
	case *ast.IndexExpr:
		// Generic: Foo[T] -> Foo
		return extractRecvTypeName(e.X)
	case *ast.IndexListExpr:
		// Generic: Foo[T, U] -> Foo
		return extractRecvTypeName(e.X)
	}
	return ""
}
