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
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	symbolMapMaxPackages = 25
	symbolMapMaxPerPkg   = 10
	// #5892 round-12: the budget gates the WHOLE scan (collect+extract).
	// A full scan of a 63-package tree takes ~205ms on an external volume;
	// at the old 200ms every periodic refresh hit the deadline, truncated
	// stayed true forever, the complete-cache was never written, and the
	// round-2/5 stale-but-stable fallback was dead code - each refresh
	// emitted a load-dependent partial count (61/62/63/64 flip). 2s keeps
	// one-shot head-assembly latency bounded while letting real trees
	// complete so the cache actually populates.
	symbolMapTimeBudget         = 2 * time.Second
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

	pkgDirs, collectTruncated := collectGoPackageDirs(root, deadline)
	if len(pkgDirs) == 0 {
		return ""
	}

	type pkgSummary struct {
		relPath   string
		symbols   []string
		fileCount int
	}

	var summaries []pkgSummary
	truncated := collectTruncated
	for _, dir := range pkgDirs {
		if time.Now().After(deadline) {
			// Ring-noise control: budget-hit fired on every periodic refresh; dropped.
			truncated = true
			_ = summaries
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

		syms, fc, extFailed := extractPackageSymbols(dir)
		if extFailed {
			// #5892 round-8: an extract-stage failure silently skipped
			// this package - the reduced set still looked COMPLETE,
			// was emitted, and poisoned the cache (61<->63 flip).
			truncated = true
		}
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

	if truncated {
		// #5892 round-2: a budget-truncated scan yields load-dependent
		// partial counts; emitting it made the head's symbols segment
		// flip between 2244/2287 symbols with machine load, rewriting the
		// whole cached prefix for no real change. Fall back to the last
		// COMPLETE scan (stale but stable); only complete scans update it.
		if cached := lastCompleteGoSymbolsSection(root); cached != "" {
			return cached
		}
		// No prior complete scan: emitting a truncated snapshot once is
		// still better than nothing; the next complete scan replaces it.
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

	text := strings.TrimRight(sb.String(), "\n")
	if !truncated {
		storeCompleteGoSymbolsSection(root, text)
	}
	return text
}

// lastCompleteGoSymbolsSection caches the most recent complete (non-
// truncated) symbols section per workspace root.
var completeGoSymbolsCache = struct {
	mu     sync.Mutex
	byRoot map[string]string
}{byRoot: map[string]string{}}

func lastCompleteGoSymbolsSection(root string) string {
	completeGoSymbolsCache.mu.Lock()
	defer completeGoSymbolsCache.mu.Unlock()
	return completeGoSymbolsCache.byRoot[root]
}

func storeCompleteGoSymbolsSection(root, text string) {
	completeGoSymbolsCache.mu.Lock()
	defer completeGoSymbolsCache.mu.Unlock()
	completeGoSymbolsCache.byRoot[root] = text
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

func collectGoPackageDirs(root string, deadline time.Time) ([]string, bool) {
	var dirs []string
	truncatedHit := false

	if has, ok := dirHasGoFiles(root); has {
		dirs = append(dirs, root)
	} else if !ok {
		truncatedHit = true
	}

	level1, err := os.ReadDir(root)
	if err != nil {
		// #5892 round-7: an unreadable root yields a dir set we cannot
		// trust - surface it as truncated so the caller falls back to
		// the last complete scan instead of emitting a partial one.
		return dirs, true
	}

	for _, e1 := range level1 {
		if !e1.IsDir() || overviewSkipDirs[e1.Name()] || strings.HasPrefix(e1.Name(), ".") {
			continue
		}
		if time.Now().After(deadline) {
			// #5892 round-5: surface truncation. A deadline hit during
			// directory collection used to return a partial dir set that
			// looked COMPLETE to the caller - the 28<->61 package
			// oscillation root (a truncated scan emitted AND poisoned the
			// complete-cache, then a full scan flipped it back).
			return dirs, true
		}

		dir1 := filepath.Join(root, e1.Name())
		if has, ok := dirHasGoFiles(dir1); has {
			dirs = append(dirs, dir1)
		} else if !ok {
			// #5892 round-10: same fourth-entrance seal at level 1.
			truncatedHit = true
		}

		level2, err := os.ReadDir(dir1)
		if err != nil {
			// #5892 round-7: transient i/o failure (e.g. external-volume
			// hiccup) silently skipped this subtree and the reduced dir
			// set still looked COMPLETE - the 63<->61 package flip root.
			// Mark truncated and keep walking; the caller emits the last
			// complete scan instead of this partial harvest.
			truncatedHit = true
			continue
		}
		for _, e2 := range level2 {

			if time.Now().After(deadline) {
				return dirs, true
			}
			if !e2.IsDir() || overviewSkipDirs[e2.Name()] || strings.HasPrefix(e2.Name(), ".") {
				continue
			}
			dir2 := filepath.Join(dir1, e2.Name())
			if has, ok := dirHasGoFiles(dir2); has {
				dirs = append(dirs, dir2)
			} else if !ok {
				// #5892 round-10: same fourth-entrance seal at level 2.
				truncatedHit = true
			}
		}
	}

	return dirs, truncatedHit
}

func dirHasGoFiles(dir string) (has, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// #5892 round-10: indistinguishable from "no .go files" was the
		// fourth silent-drop entrance - a transient i/o failure here
		// dropped the whole directory with no truncation signal.
		return false, false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true, true
		}
	}
	return false, true
}

func extractPackageSymbols(dir string) ([]string, int, bool) {
	fset := token.NewFileSet()
	failed := false
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
		// #5892 round-8: a parse failure means part of the package's
		// syntax tree is missing - the harvest is not trustworthy.
		failed = true
		debug.Log("agentruntime", "symbol map: parse error in %s: %v", filepath.Base(dir), firstErr)
	}

	if len(pkgs) == 0 {
		// Directories reach here via dirHasGoFiles, so zero packages with
		// files counted means every parse failed (e.g. transient i/o).
		// Zero with nothing counted can be a stale directory listing.
		return nil, 0, true
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
		return nil, parsedFiles, failed
	}

	symbols := make([]string, 0, len(seen))
	for s := range seen {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols, parsedFiles, failed
}
