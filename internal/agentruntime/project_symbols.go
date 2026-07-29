package agentruntime

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Symbol-level codebase awareness for the system prompt.
//
// Research basis: Aider's "repo map" uses tree-sitter to rank and display the
// most important symbols (classes, functions, methods) from the codebase,
// giving the LLM structural awareness without reading every file. This yields
// measurable improvements in navigation and edit accuracy because the model
// knows which files define which symbols before deciding where to make changes.
//
// ggcode already has a directory-level "Project layout" section and LSP tools
// for on-demand symbol lookup, but neither gives the model a zero-cost overview
// of package APIs at session start. This module fills that gap for Go projects
// by parsing .go files with go/ast and extracting exported type and function
// declarations into a compact, budget-limited summary injected into the system
// prompt.

const (
	// symbolMapMaxPackages caps the number of packages shown to keep the system
	// prompt small even in monorepos with hundreds of packages.
	symbolMapMaxPackages = 25
	// symbolMapMaxPerPkg limits symbols listed per package line.
	symbolMapMaxPerPkg = 10
	// symbolMapTimeBudget is the maximum wall-clock time spent parsing. This
	// ensures system-prompt construction stays fast even for large repos.
	symbolMapTimeBudget = 200 * time.Millisecond
)

// buildGoPackageSymbolsSection generates a compact summary of exported Go
// types and functions for each package in the workspace. Returns an empty
// string if the project is not Go or has no parseable packages.
//
// The summary is injected into the system prompt after the project layout and
// commands sections, giving the model symbol-level awareness without any tool
// calls.
func buildGoPackageSymbolsSection(root string) string {
	if root == "" {
		return ""
	}

	// Quick pre-check: bail out immediately for non-Go projects to avoid
	// wasting time scanning directories.
	if !hasGoSource(root) {
		return ""
	}

	deadline := time.Now().Add(symbolMapTimeBudget)

	pkgDirs := collectGoPackageDirs(root, deadline)
	if len(pkgDirs) == 0 {
		return ""
	}

	type pkgSummary struct {
		relPath   string
		symbols   []string
		fileCount int
	}

	var summaries []pkgSummary
	for _, dir := range pkgDirs {
		if time.Now().After(deadline) {
			debug.Log("agentruntime", "symbol map: time budget hit after %d packages", len(summaries))
			break
		}

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = "(root)"
		}

		syms, fc := extractPackageSymbols(dir)
		if len(syms) == 0 {
			continue
		}

		summaries = append(summaries, pkgSummary{
			relPath:   rel,
			symbols:   syms,
			fileCount: fc,
		})
	}

	if len(summaries) == 0 {
		return ""
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].relPath < summaries[j].relPath
	})

	var sb strings.Builder
	sb.WriteString("\n\n## Package symbols\n")
	sb.WriteString("Key exported declarations per Go package. Use lsp_symbols, lsp_definition, or read_file for details.\n")

	shown := 0
	for _, pkg := range summaries {
		if shown >= symbolMapMaxPackages {
			sb.WriteString("  ... (more packages omitted)\n")
			break
		}

		syms := pkg.symbols
		suffix := ""
		if len(syms) > symbolMapMaxPerPkg {
			syms = syms[:symbolMapMaxPerPkg]
			suffix = ", ..."
		}

		label := pkg.relPath + "/"
		sb.WriteString(fmt.Sprintf("  %s (%d files: %s%s)\n", label, pkg.fileCount, strings.Join(syms, ", "), suffix))
		shown++
	}

	return strings.TrimRight(sb.String(), "\n")
}

// hasGoSource performs a fast two-level check for any .go file to decide
// whether symbol-map construction is worthwhile.
func hasGoSource(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	for _, e := range entries {
		if !e.IsDir() || overviewSkipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, s := range sub {
			if !s.IsDir() && strings.HasSuffix(s.Name(), ".go") {
				return true
			}
		}
	}
	return false
}

// collectGoPackageDirs finds directories at depth 0–2 that contain non-test
// Go source files. It respects overviewSkipDirs and hidden directories.
func collectGoPackageDirs(root string, deadline time.Time) []string {
	var dirs []string

	// Root level
	if dirHasGoFiles(root) {
		dirs = append(dirs, root)
	}

	level1, err := os.ReadDir(root)
	if err != nil {
		return dirs
	}

	for _, e1 := range level1 {
		if !e1.IsDir() || overviewSkipDirs[e1.Name()] || strings.HasPrefix(e1.Name(), ".") {
			continue
		}
		if time.Now().After(deadline) {
			return dirs
		}

		dir1 := filepath.Join(root, e1.Name())
		if dirHasGoFiles(dir1) {
			dirs = append(dirs, dir1)
		}

		// Depth 2
		level2, err := os.ReadDir(dir1)
		if err != nil {
			continue
		}
		for _, e2 := range level2 {
			if !e2.IsDir() || overviewSkipDirs[e2.Name()] || strings.HasPrefix(e2.Name(), ".") {
				continue
			}
			dir2 := filepath.Join(dir1, e2.Name())
			if dirHasGoFiles(dir2) {
				dirs = append(dirs, dir2)
			}
		}
	}

	return dirs
}

// dirHasGoFiles reports whether a directory contains at least one non-test
// .go file.
func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
	}
	return false
}

// extractPackageSymbols parses all non-test .go files in a directory and
// returns a sorted, deduplicated list of exported type and function names,
// plus the number of files parsed.
func extractPackageSymbols(dir string) ([]string, int) {
	fset := token.NewFileSet()
	pkgs, firstErr := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		name := fi.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.SkipObjectResolution)

	// Even if ParseDir returns an error, it may still return partial ASTs.
	// We log the error but proceed with whatever we got.
	if firstErr != nil {
		debug.Log("agentruntime", "symbol map: parse error in %s: %v", filepath.Base(dir), firstErr)
	}

	if len(pkgs) == 0 {
		return nil, 0
	}

	seen := make(map[string]bool)
	fileCount := 0

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			fileCount++
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					// Only top-level exported functions (skip methods to
					// keep the list compact — the receiver type is already
					// listed as a type symbol).
					if d.Recv == nil && d.Name.IsExported() {
						seen[d.Name.Name+"()"] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
							seen[ts.Name.Name] = true
						}
					}
				}
			}
		}
	}

	if len(seen) == 0 {
		return nil, fileCount
	}

	symbols := make([]string, 0, len(seen))
	for s := range seen {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols, fileCount
}
