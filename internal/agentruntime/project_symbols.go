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

const (
	symbolMapMaxPackages        = 25
	symbolMapMaxPerPkg          = 10
	symbolMapTimeBudget         = 200 * time.Millisecond
	symbolMapMaxFilesPerPackage = 200
)

func buildGoPackageSymbolsSection(root string) string {
	if root == "" {
		return ""
	}

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
	// P3 (#5446): counts + largest packages only. Full export lists moved to
	// the on-demand `symbols` tool (registered with the same extractors).
	totalSyms := 0
	for _, pkg := range summaries {
		totalSyms += len(pkg.symbols)
	}
	sort.SliceStable(summaries, func(i, j int) bool { return len(summaries[i].symbols) > len(summaries[j].symbols) })
	sb.WriteString(fmt.Sprintf("%d Go packages, %d exported symbols. Largest: ", len(summaries), totalSyms))
	for i, pkg := range summaries {
		if i >= 8 {
			break
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s (%d)", pkg.relPath, len(pkg.symbols)))
	}
	sb.WriteString("\nQuery the `symbols` tool (matches package paths and symbol names) for a package's exports; use lsp_workspace_symbols/lsp_definition for positions.\n")

	return strings.TrimRight(sb.String(), "\n")
}

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

func collectGoPackageDirs(root string, deadline time.Time) []string {
	var dirs []string

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

		level2, err := os.ReadDir(dir1)
		if err != nil {
			continue
		}
		for _, e2 := range level2 {

			if time.Now().After(deadline) {
				return dirs
			}
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

func extractPackageSymbols(dir string) ([]string, int) {
	fset := token.NewFileSet()

	fileCount := 0
	pkgs, firstErr := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		name := fi.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return false
		}
		fileCount++
		return fileCount <= symbolMapMaxFilesPerPackage
	}, parser.SkipObjectResolution)

	if firstErr != nil {
		debug.Log("agentruntime", "symbol map: parse error in %s: %v", filepath.Base(dir), firstErr)
	}

	if len(pkgs) == 0 {
		return nil, 0
	}

	seen := make(map[string]bool)
	parsedFiles := 0

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			parsedFiles++
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:

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
		return nil, parsedFiles
	}

	symbols := make([]string, 0, len(seen))
	for s := range seen {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols, parsedFiles
}
