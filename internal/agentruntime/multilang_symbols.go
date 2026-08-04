package agentruntime

// Multi-language symbol extraction for the system prompt.
//
// This complements buildGoPackageSymbolsSection which only handles Go.
// Many ggcode users work with TypeScript/JavaScript or Python projects that
// get zero symbol-level awareness in the system prompt. This module adds
// lightweight regex-based extraction for those languages -- no external
// dependencies (tree-sitter, language servers), consistent with the Go path's
// pure-stdlib approach.
//
// Design:
//   - Same time budget as Go symbol extraction (200ms).
//   - Same depth limit (0-2 directory levels).
//   - Same output format for prompt consistency.
//   - Regex patterns target exported/public declarations only:
//       TypeScript/JS: export function, export class, export const, export type
//       Python: top-level def, class (convention: no leading underscore = public)
//   - Skips test files, node_modules, __pycache__, venv, .venv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// --- TypeScript/JavaScript ---

// hasTSSource performs a fast two-level check for any .ts/.tsx/.js/.jsx file.
func hasTSSource(root string) bool {
	return hasSourceWithSuffixes(root, []string{".ts", ".tsx", ".js", ".jsx"})
}

func hasPythonSource(root string) bool {
	return hasSourceWithSuffixes(root, []string{".py"})
}

func hasSourceWithSuffixes(root string, suffixes []string) bool {
	has := func(name string) bool {
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				return true
			}
		}
		return false
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && has(e.Name()) {
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
			if !s.IsDir() && has(s.Name()) {
				return true
			}
			// Depth 2
			if !s.IsDir() || overviewSkipDirs[s.Name()] || strings.HasPrefix(s.Name(), ".") {
				continue
			}
			sub2, err := os.ReadDir(filepath.Join(root, e.Name(), s.Name()))
			if err != nil {
				continue
			}
			for _, s2 := range sub2 {
				if !s2.IsDir() && has(s2.Name()) {
					return true
				}
			}
		}
	}
	return false
}

// TS/JS export patterns.
// Matches: export function foo, export const bar, export class Baz, export type Qux
var (
	tsExportFuncRe  = regexp.MustCompile(`^\s*export\s+(?:async\s+)?function\s+(\w+)`)
	tsExportClassRe = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:abstract\s+)?class\s+(\w+)`)
	tsExportTypeRe  = regexp.MustCompile(`^\s*export\s+(?:type|interface|enum)\s+(\w+)`)
	tsExportConstRe = regexp.MustCompile(`^\s*export\s+(?:const|let|var)\s+(\w+)`)
)

// jsSuffixes are the file extensions we treat as JS/TS source.
var jsSuffixes = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true}

// buildTSSymbolsSection generates a compact summary of exported declarations
// for TypeScript/JavaScript projects. Returns "" if the project has no TS/JS
// source or if no symbols are found.
func buildTSSymbolsSection(root string) string {
	return buildMultilangSymbolsSection(root, "ts", "Package symbols",
		"TypeScript/JavaScript", hasTSSource, extractTSSymbols)
}

// buildPythonSymbolsSection generates a compact summary of top-level
// declarations for Python projects.
func buildPythonSymbolsSection(root string) string {
	return buildMultilangSymbolsSection(root, "py", "Package symbols",
		"Python", hasPythonSource, extractPythonSymbols)
}

// buildMultilangSymbolsSection is the shared driver for non-Go symbol extraction.
// It mirrors the Go path's structure: collect dirs at depth 0-2, extract symbols
// per directory, render a compact summary with a package cap.
func buildMultilangSymbolsSection(
	root string,
	langTag string,
	sectionTitle string,
	langLabel string,
	hasSource func(string) bool,
	extract func(string, time.Time) ([]string, int),
) string {
	if root == "" {
		return ""
	}
	if !hasSource(root) {
		return ""
	}

	deadline := time.Now().Add(symbolMapTimeBudget)

	pkgDirs := collectMultilangDirs(root, langTag, deadline)
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
			debug.Log("agentruntime", "%s symbol map: time budget hit after %d dirs", langLabel, len(summaries))
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

		syms, fc := extract(dir, deadline)
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
	sb.WriteString("\n\n")
	sb.WriteString("## ")
	sb.WriteString(sectionTitle)
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Key exported declarations per %s module. Use lsp_symbols, lsp_definition, or read_file for details.\n", langLabel))

	shown := 0
	for _, pkg := range summaries {
		if shown >= symbolMapMaxPackages {
			sb.WriteString("  ... (more modules omitted)\n")
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

// collectMultilangDirs finds directories at depth 0-2 that contain source files
// of the given language tag ("ts" or "py"). Same skip rules as the Go path.
func collectMultilangDirs(root string, langTag string, deadline time.Time) []string {
	var dirs []string

	hasFiles := func(dir string) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if isMultilangSourceFile(name, langTag) {
				return true
			}
		}
		return false
	}

	// Root level
	if hasFiles(root) {
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
		if hasFiles(dir1) {
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
			if hasFiles(dir2) {
				dirs = append(dirs, dir2)
			}
		}
	}

	return dirs
}

// isMultilangSourceFile reports whether a filename is a parseable source file
// for the given language tag. Excludes test files and config files.
func isMultilangSourceFile(name string, langTag string) bool {
	switch langTag {
	case "ts":
		if !jsSuffixes[strings.ToLower(filepath.Ext(name))] {
			return false
		}
		// Skip .d.ts declaration files
		if strings.HasSuffix(name, ".d.ts") {
			return false
		}
		// Skip test files
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") {
			return false
		}
		return true
	case "py":
		if !strings.HasSuffix(name, ".py") {
			return false
		}
		// Skip __init__.py and test files
		if name == "__init__.py" {
			return false
		}
		if strings.HasSuffix(name, "_test.py") || strings.HasSuffix(name, "_conftest.py") {
			return false
		}
		if strings.HasPrefix(name, "test_") || strings.HasPrefix(name, "conftest") {
			return false
		}
		return true
	}
	return false
}

// extractTSSymbols parses all TS/JS source files in a directory and returns
// a sorted, deduplicated list of exported declaration names.
func extractTSSymbols(dir string, deadline time.Time) ([]string, int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}

	seen := make(map[string]bool)
	fileCount := 0

	for _, e := range entries {
		if e.IsDir() || !isMultilangSourceFile(e.Name(), "ts") {
			continue
		}
		if time.Now().After(deadline) {
			break
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		fileCount++

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			// Only scan the first ~1000 chars of each line to avoid pathological cases
			if len(line) > 1000 {
				line = line[:1000]
			}

			if m := tsExportFuncRe.FindStringSubmatch(line); m != nil {
				seen[m[1]+"()"] = true
				continue
			}
			if m := tsExportClassRe.FindStringSubmatch(line); m != nil {
				seen[m[1]] = true
				continue
			}
			if m := tsExportTypeRe.FindStringSubmatch(line); m != nil {
				seen[m[1]] = true
				continue
			}
			if m := tsExportConstRe.FindStringSubmatch(line); m != nil {
				// Only include const exports that look like important constants
				// (uppercase or single-word identifiers) to avoid noise from
				// many small const exports.
				name := m[1]
				if isLikelyImportantExport(name) {
					seen[name] = true
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

// Python patterns.
var (
	pyFuncRe  = regexp.MustCompile(`^\s*def\s+(\w+)`)
	pyClassRe = regexp.MustCompile(`^\s*class\s+(\w+)`)
)

// extractPythonSymbols parses all Python source files in a directory and returns
// a sorted, deduplicated list of top-level public declarations (functions and
// classes without a leading underscore).
func extractPythonSymbols(dir string, deadline time.Time) ([]string, int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}

	seen := make(map[string]bool)
	fileCount := 0

	for _, e := range entries {
		if e.IsDir() || !isMultilangSourceFile(e.Name(), "py") {
			continue
		}
		if time.Now().After(deadline) {
			break
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		fileCount++

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if len(line) > 1000 {
				line = line[:1000]
			}

			// Only top-level (no indent) declarations are public API
			trimmed := strings.TrimLeft(line, " \t")
			if len(line) == len(trimmed) {
				// No indent -- top-level
				if m := pyFuncRe.FindStringSubmatch(line); m != nil {
					name := m[1]
					if !strings.HasPrefix(name, "_") {
						seen[name+"()"] = true
					}
					continue
				}
				if m := pyClassRe.FindStringSubmatch(line); m != nil {
					name := m[1]
					if !strings.HasPrefix(name, "_") {
						seen[name] = true
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

// isLikelyImportantExport heuristically decides whether a const export name is
// significant enough to show in the symbol map. Filters out trivial exports
// like `export const x = 1` while keeping `export const MAX_RETRIES = 5` or
// `export const AppConfig`.
func isLikelyImportantExport(name string) bool {
	if len(name) < 2 {
		return false
	}
	// UPPERCASE names (constants) are always interesting
	if name == strings.ToUpper(name) && name != strings.ToLower(name) {
		return true
	}
	// PascalCase names (likely constructors / config objects)
	first := name[0]
	if first >= 'A' && first <= 'Z' {
		return true
	}
	// camelCase single words are too noisy (export const items = [])
	return false
}
