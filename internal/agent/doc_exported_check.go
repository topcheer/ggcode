package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Documentation Intelligence: write-time exported-doc health check.
//
// Detects Go exported identifiers (functions, types, vars, consts) that were
// ADDED by the current edit but lack a godoc comment. This mirrors golint /
// revive's "exported should have comment" rule, but fires at write time on the
// delta - only newly-introduced undocumented exports are flagged, so the agent
// is never flooded with pre-existing documentation debt.
//
// Design rationale:
//   - Zero LLM cost: pure AST analysis via go/parser.
//   - Write-time advantage: competitors (golint, Swimm, Mintlify) scan after
//     the fact; we catch the missing doc at the moment of creation.
//   - Diff-aware: compares old vs new content to isolate newly-added exports,
//     avoiding noise from legacy undocumented code.

const (
	maxExportedDocWarnings = 3
)

// docMissingInfo describes an exported identifier that lacks a godoc comment.
type docMissingInfo struct {
	kind string // "function", "type", "variable", "constant"
	name string
}

// checkMissingExportedDocsAST detects newly-added exported identifiers without
// godoc comments. It uses the pre-parsed new-content AST (from CheckContext)
// and parses old content to determine what was already present.
func checkMissingExportedDocsAST(filePath, oldContent string, newAST *ast.File) []string {
	if newAST == nil {
		return nil
	}

	// Skip test files — exported test helpers don't need godoc.
	if isTestFile(filePath) {
		return nil
	}

	// Skip main packages — exported identifiers in main aren't part of a
	// public API (e.g. cmd/foo/main.go).
	if newAST.Name != nil && newAST.Name.Name == "main" {
		return nil
	}

	newMissing := collectExportedWithoutDocs(newAST)
	if len(newMissing) == 0 {
		return nil
	}

	oldMissing := collectExportedWithoutDocsSrc(oldContent)

	var warnings []string
	seen := make(map[string]bool, len(newMissing))
	for key, info := range newMissing {
		if _, existed := oldMissing[key]; existed {
			continue // was already missing a doc before this edit
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		warnings = append(warnings, formatExportedDocWarning(info))
		if len(warnings) >= maxExportedDocWarnings {
			break
		}
	}

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// collectExportedWithoutDocs gathers exported identifiers lacking godoc from a
// parsed AST file.
func collectExportedWithoutDocs(file *ast.File) map[string]docMissingInfo {
	result := make(map[string]docMissingInfo)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Skip methods on unexported receiver types — they can't be
			// accessed externally regardless of method name casing.
			if isUnexportedReceiverMethod(d) {
				continue
			}
			if d.Recv == nil && d.Name.IsExported() && d.Doc == nil {
				result["func:"+d.Name.Name] = docMissingInfo{kind: "function", name: d.Name.Name}
			}
		case *ast.GenDecl:
			collectGenDeclMissingDocs(d, result)
		}
	}
	return result
}

// collectExportedWithoutDocsSrc parses source text and collects undocumented
// exports. Returns empty map on parse error (old content may be mid-edit).
func collectExportedWithoutDocsSrc(src string) map[string]docMissingInfo {
	if strings.TrimSpace(src) == "" {
		return make(map[string]docMissingInfo)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return make(map[string]docMissingInfo)
	}
	return collectExportedWithoutDocs(file)
}

// isUnexportedReceiverMethod returns true for methods like (t *myType) Exported()
// where the receiver type is unexported — the method is not truly reachable.
func isUnexportedReceiverMethod(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return false
	}
	star, ok := d.Recv.List[0].Type.(*ast.StarExpr)
	if ok {
		if id, ok := star.X.(*ast.Ident); ok {
			return !id.IsExported()
		}
	}
	if id, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
		return !id.IsExported()
	}
	return false
}

// collectGenDeclMissingDocs inspects type/var/const declarations for missing
// docs on exported names.
func collectGenDeclMissingDocs(d *ast.GenDecl, result map[string]docMissingInfo) {
	multiSpec := len(d.Specs) > 1
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			if genDeclHasDoc(d, s, multiSpec) {
				continue
			}
			result["type:"+s.Name.Name] = docMissingInfo{kind: "type", name: s.Name.Name}
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if !name.IsExported() {
					continue
				}
				if genDeclHasDoc(d, s, multiSpec) {
					continue
				}
				kind := "variable"
				if d.Tok == token.CONST {
					kind = "constant"
				}
				result["var:"+name.Name] = docMissingInfo{kind: kind, name: name.Name}
			}
		}
	}
}

// genDeclHasDoc checks whether a spec within a GenDecl has a doc comment.
// For single-spec declarations, the doc can be on the GenDecl or the spec.
// For multi-spec grouped declarations, each spec needs its own doc.
func genDeclHasDoc(decl *ast.GenDecl, specDoc ast.Node, multiSpec bool) bool {
	if multiSpec {
		return specDocHasComment(specDoc)
	}
	return decl.Doc != nil || specDocHasComment(specDoc)
}

func specDocHasComment(spec ast.Node) bool {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc != nil
	case *ast.ValueSpec:
		return s.Doc != nil
	}
	return false
}

// formatExportedDocWarning renders a concise, actionable warning.
func formatExportedDocWarning(info docMissingInfo) string {
	return fmt.Sprintf(
		"Exported %s %q was added without a godoc comment. "+
			"Go convention requires exported identifiers to be documented "+
			"(golint/revive). Add a comment starting with %q above the declaration.",
		info.kind, info.name, info.name)
}
