package agentruntime

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

func hasTSSource(root string) bool {
	return hasSourceWithSuffixes(root, []string{".ts", ".tsx", ".js", ".jsx"})
}

func hasPythonSource(root string) bool {
	return hasSourceWithSuffixes(root, []string{".py"})
}

func hasSourceWithSuffixes(root string, suffixes []string) bool {
	match := func(name string) bool {
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				return true
			}
		}
		return false
	}

	if dirContainsMatchingFile(root, match) {
		return true
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e1 := range entries {
		if !e1.IsDir() || skipScanDir(e1.Name()) {
			continue
		}
		dir1 := filepath.Join(root, e1.Name())
		if dirContainsMatchingFile(dir1, match) {
			return true
		}

		sub, err := os.ReadDir(dir1)
		if err != nil {
			continue
		}
		for _, e2 := range sub {
			if !e2.IsDir() || skipScanDir(e2.Name()) {
				continue
			}
			if dirContainsMatchingFile(filepath.Join(dir1, e2.Name()), match) {
				return true
			}
		}
	}
	return false
}

func dirContainsMatchingFile(dir string, match func(string) bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && match(e.Name()) {
			return true
		}
	}
	return false
}

func skipScanDir(name string) bool {
	return overviewSkipDirs[name] || strings.HasPrefix(name, ".")
}

var (
	tsExportFuncRe  = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?function\s+(\w+)`)
	tsExportClassRe = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:abstract\s+)?class\s+(\w+)`)
	tsExportTypeRe  = regexp.MustCompile(`^\s*export\s+(?:type|interface|enum)\s+(\w+)`)
	tsExportConstRe = regexp.MustCompile(`^\s*export\s+(?:const|let|var)\s+(\w+)`)
)

var jsSuffixes = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true}

func buildTSSymbolsSection(root string) string {
	return buildMultilangSymbolsSection(root, "ts", "Package symbols",
		"TypeScript/JavaScript", hasTSSource, extractTSSymbols)
}

func buildPythonSymbolsSection(root string) string {
	return buildMultilangSymbolsSection(root, "py", "Package symbols",
		"Python", hasPythonSource, extractPythonSymbols)
}

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
	// P3 (#5446): counts + largest modules only; full export lists moved to
	// the on-demand `symbols` tool (same extractors, query by path/symbol).
	totalSyms := 0
	for _, pkg := range summaries {
		totalSyms += len(pkg.symbols)
	}
	sort.SliceStable(summaries, func(i, j int) bool { return len(summaries[i].symbols) > len(summaries[j].symbols) })
	sb.WriteString(fmt.Sprintf("%d %s modules, %d exported symbols. Largest: ", len(summaries), langLabel, totalSyms))
	for i, pkg := range summaries {
		if i >= 8 {
			break
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s (%d)", pkg.relPath, len(pkg.symbols)))
	}
	sb.WriteString(fmt.Sprintf("\nQuery the `symbols` tool (language=%s) for a module's exports.\n", langTag))

	return strings.TrimRight(sb.String(), "\n")
}

func collectMultilangDirs(root string, langTag string, deadline time.Time) []string {
	var dirs []string

	hasFiles := func(dir string) bool {
		return dirContainsMatchingFile(dir, func(name string) bool {
			return isMultilangSourceFile(name, langTag)
		})
	}

	if hasFiles(root) {
		dirs = append(dirs, root)
	}

	level1, err := os.ReadDir(root)
	if err != nil {
		return dirs
	}

	for _, e1 := range level1 {
		if !e1.IsDir() || skipScanDir(e1.Name()) {
			continue
		}
		if time.Now().After(deadline) {
			return dirs
		}

		dir1 := filepath.Join(root, e1.Name())
		if hasFiles(dir1) {
			dirs = append(dirs, dir1)
		}

		level2, err := os.ReadDir(dir1)
		if err != nil {
			continue
		}
		for _, e2 := range level2 {
			if !e2.IsDir() || skipScanDir(e2.Name()) {
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

func isMultilangSourceFile(name string, langTag string) bool {
	switch langTag {
	case "ts":
		if !jsSuffixes[strings.ToLower(filepath.Ext(name))] {
			return false
		}

		if strings.HasSuffix(name, ".d.ts") {
			return false
		}

		base := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") {
			return false
		}
		return true
	case "py":
		if !strings.HasSuffix(name, ".py") {
			return false
		}

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

var (
	pyFuncRe  = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	pyClassRe = regexp.MustCompile(`^\s*class\s+(\w+)`)
)

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

			trimmed := strings.TrimLeft(line, " \t")
			if len(line) == len(trimmed) {

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

func isLikelyImportantExport(name string) bool {
	if len(name) < 2 {
		return false
	}

	if name == strings.ToUpper(name) && name != strings.ToLower(name) {
		return true
	}

	first := name[0]
	if first >= 'A' && first <= 'Z' {
		return true
	}

	return false
}
