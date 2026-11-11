package agentruntime

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// workspaceSymbols implements tool.SymbolsProvider (#5446 P3): on-demand
// package export lists backed by the same extractors that used to inline the
// full symbol maps into the system prompt.
type workspaceSymbols struct {
	root string
}

func matchesQuery(q, path string, symbols []string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(path), q) {
		return true
	}
	for _, s := range symbols {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

func (w workspaceSymbols) Symbols(lang, query string, limit int) ([]tool.PackageSymbols, error) {
	if w.root == "" {
		return nil, fmt.Errorf("no workspace root")
	}
	if limit <= 0 {
		limit = 12
	}
	deadline := time.Now().Add(symbolMapTimeBudget)
	var out []tool.PackageSymbols

	addLang := func(tag string, dirs []string, extract func(string) ([]string, int)) {
		for _, dir := range dirs {
			if time.Now().After(deadline) {
				return
			}
			syms, _ := extract(dir)
			if len(syms) == 0 {
				continue
			}
			rel, err := filepath.Rel(w.root, dir)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				rel = "(root)"
			}
			if !matchesQuery(query, rel, syms) {
				continue
			}
			out = append(out, tool.PackageSymbols{Language: tag, Path: rel, Symbols: syms})
		}
	}

	if lang == "" || lang == "go" {
		addLang("go", collectGoPackageDirs(w.root, deadline), extractPackageSymbols)
	}
	if lang == "" || lang == "ts" {
		if hasTSSource(w.root) {
			addLang("ts", collectMultilangDirs(w.root, "ts", deadline), func(dir string) ([]string, int) {
				return extractTSSymbols(dir, deadline)
			})
		}
	}
	if lang == "" || lang == "py" {
		if hasPythonSource(w.root) {
			addLang("py", collectMultilangDirs(w.root, "py", deadline), func(dir string) ([]string, int) {
				return extractPythonSymbols(dir, deadline)
			})
		}
	}

	// empty query = largest packages first; a query ranks matches by count too
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Symbols) > len(out[j].Symbols) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// multilangSourceDirs returns the root-level source dirs for a language
// (the multilang builders scan a small set of well-known roots).
func multilangSourceDirs(root string, has func(string) bool) []string {
	if has(root) {
		return []string{root}
	}
	return nil
}
