package tool

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// formatGoBytes applies gofmt-style formatting AND automatic unused-import removal
// to Go source files (.go).
//
// Non-Go files and code that fails to parse (e.g. an edit-in-progress or a
// generated fragment) are returned unchanged with changed=false — this never
// corrupts the agent's output. Returns the (possibly reformatted) bytes and
// whether formatting changed the content.
//
// In addition to gofmt, this removes unused imports — the #1 cause of build
// failures after agent edits. Competitors: Aider runs goimports automatically,
// Cursor auto-removes unused imports. This closes the gap for ggcode using
// zero external dependencies (go/ast + go/format from the standard library).
//
// Only unused imports are removed. Missing imports are NOT auto-added because
// guessing the correct package path from a short identifier is error-prone
// (the import_lint.go warning in the agent package handles that case).
func formatGoBytes(path string, data []byte) ([]byte, bool) {
	if filepath.Ext(path) != ".go" {
		return data, false
	}

	result := data
	changed := false

	// Step 1: gofmt
	if formatted, err := format.Source(result); err == nil {
		if !bytes.Equal(formatted, result) {
			result = formatted
			changed = true
		}
	} else {
		// Can't parse — can't safely organize imports either.
		return data, false
	}

	// Step 2: Remove unused imports (zero-cost AST analysis)
	cleaned, removedCount := removeUnusedGoImports(result)
	if removedCount > 0 {
		result = cleaned
		changed = true
		debug.Log("tool", "auto-organize-imports: %s removed %d unused import(s)", filepath.Base(path), removedCount)
	}

	if !changed {
		return data, false
	}

	debug.Log("tool", "auto-format: %s (%d → %d bytes)", filepath.Base(path), len(data), len(result))
	return result, true
}

// removeUnusedGoImports parses Go source, identifies imports whose package
// identifier is never referenced outside the import block, removes them, and
// returns the cleaned source. Uses go/ast for precise analysis (<1ms per file).
//
// Returns the (possibly modified) bytes and the count of imports removed.
// If parsing fails or no imports are unused, returns the input unchanged with
// count=0.
//
// Safety guarantees:
//   - Blank (_) and dot (.) imports are always kept (side-effect imports).
//   - Files with syntax errors are returned unchanged (no corruption risk).
//   - Comments are preserved (format.Node handles comment association).
func removeUnusedGoImports(data []byte) ([]byte, int) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", data, parser.ParseComments)
	if err != nil {
		return data, 0
	}

	// Collect all identifiers used outside import declarations.
	usedIdents := make(map[string]bool)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				usedIdents[ident.Name] = true
			}
			return true
		})
	}

	removedCount := 0
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}

		var filtered []ast.Spec
		for _, spec := range genDecl.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				filtered = append(filtered, spec)
				continue
			}

			name := effectiveImportName(imp)
			// Keep blank (_) and dot (.) imports — they have side effects.
			if name == "_" || name == "." {
				filtered = append(filtered, spec)
				continue
			}

			if usedIdents[name] {
				filtered = append(filtered, spec)
			} else {
				removedCount++
			}
		}
		genDecl.Specs = filtered
	}

	if removedCount == 0 {
		return data, 0
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		// If printing fails, return original data unchanged (safe fallback).
		return data, 0
	}
	return buf.Bytes(), removedCount
}

// effectiveImportName returns the identifier through which an import is
// referenced in code: the explicit alias if present, otherwise the last
// non-version component of the import path.
//
// For versioned module paths like "charm.land/lipgloss/v2", the last segment
// "v2" is a module version indicator, not the package name. The actual package
// name is "lipgloss" (the segment before the version). Without this correction,
// the import remover would look for an identifier "v2" in the code, fail to
// find it, and wrongly delete the import — breaking the build.
func effectiveImportName(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	rawPath := strings.Trim(imp.Path.Value, `"`)
	parts := strings.Split(rawPath, "/")
	last := parts[len(parts)-1]
	// If the last segment is a version indicator (v2, v3, ...), use the
	// segment before it, which is the actual package name.
	if isVersionSegment(last) && len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return last
}

// isVersionSegment reports whether s looks like a Go module version path
// segment (e.g. "v2", "v3").
func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
