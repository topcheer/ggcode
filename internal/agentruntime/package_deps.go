package agentruntime

// Package dependency graph awareness for the system prompt.
//
// Research basis: RepoHyper (Jin et al. 2024) and MapCoder demonstrate that
// dependency-graph-augmented code retrieval significantly improves navigation
// and edit accuracy. Aider's "repo map" gives symbol-level awareness but not
// inter-package relationships. CodeRabbit and GraphRAG-based tools use call
// graphs to understand blast radius.
//
// ggcode already injects:
//   - Project layout (directory tree, depth 2)
//   - Package symbols (exported types/funcs per Go package)
//
// What's missing: which packages import which other packages. The agent knows
// WHAT exists in each package but not HOW packages connect. Without this, the
// agent cannot assess the blast radius of an edit (e.g., "changing config.Config
// affects 12 packages") or navigate efficiently to where a type is used.
//
// This module builds a compact internal-package dependency graph at session
// start by parsing Go import declarations. It renders the most-depended-upon
// ("hub") packages with their incoming edge counts — giving the agent immediate
// blast-radius awareness in one line of prompt budget.
//
// Design:
//   - Zero LLM cost (pure Go AST parsing, reuses collectGoPackageDirs).
//   - Time-budgeted (200ms cap, same as symbol map).
//   - Only internal imports counted (those within the module path from go.mod).
//   - Renders as a single compact line listing top hub packages by fan-in.

import (
	"fmt"
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
	// depGraphMaxHubs limits how many hub packages are shown.
	depGraphMaxHubs = 10
	// depGraphTimeBudget matches the symbol map budget for consistency.
	depGraphTimeBudget = 200 * time.Millisecond
	// depGraphMinFanIn only shows packages imported by at least N others to
	// avoid listing leaf packages with no dependents.
	depGraphMinFanIn = 2
)

// buildPackageDepsSection generates a compact summary of the internal package
// import graph, showing the most-depended-upon hub packages with their fan-in
// counts. Returns an empty string for non-Go projects or if no edges are found.
func buildPackageDepsSection(root string) string {
	if root == "" {
		return ""
	}

	modulePath := readModulePath(root)
	if modulePath == "" {
		return ""
	}

	deadline := time.Now().Add(depGraphTimeBudget)
	pkgDirs := collectGoPackageDirs(root, deadline)
	if len(pkgDirs) == 0 {
		return ""
	}

	dirToImport, importToDir := buildImportMaps(root, modulePath, pkgDirs)
	fanIn := computeFanIn(pkgDirs, modulePath, dirToImport, importToDir, deadline)

	hubs := filterAndSortHubs(fanIn)
	if len(hubs) == 0 {
		return ""
	}

	return renderDepsSection(hubs, modulePath)
}

// buildImportMaps creates bidirectional mappings between directory paths and
// Go import paths for all package directories in the module.
func buildImportMaps(root, modulePath string, pkgDirs []string) (dirToImport, importToDir map[string]string) {
	dirToImport = make(map[string]string, len(pkgDirs))
	importToDir = make(map[string]string, len(pkgDirs))
	for _, dir := range pkgDirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel != "." {
			rel = "/" + rel
		} else {
			rel = ""
		}
		imp := modulePath + rel
		dirToImport[dir] = imp
		importToDir[imp] = dir
	}
	return dirToImport, importToDir
}

// depHub represents a package with its incoming dependency count.
type depHub struct {
	imp   string
	count int
}

// computeFanIn counts how many internal packages import each package.
// Only imports targeting known internal packages are counted.
func computeFanIn(pkgDirs []string, modulePath string, dirToImport, importToDir map[string]string, deadline time.Time) map[string]int {
	fanIn := make(map[string]int)

	for _, dir := range pkgDirs {
		if time.Now().After(deadline) {
			debug.Log("agentruntime", "dep graph: time budget hit after %d packages", len(fanIn))
			break
		}

		imports := parsePackageImports(dir)
		for _, imp := range imports {
			if !isInternalImport(imp, modulePath) {
				continue
			}
			if dirToImport[dir] == imp {
				continue // skip self-imports
			}
			if _, ok := importToDir[imp]; ok {
				fanIn[imp]++
			}
		}
	}

	return fanIn
}

// isInternalImport reports whether an import path belongs to the module.
func isInternalImport(imp, modulePath string) bool {
	return strings.HasPrefix(imp, modulePath+"/") || imp == modulePath
}

// filterAndSortHubs returns packages with fan-in >= depGraphMinFanIn, sorted by
// fan-in descending (alphabetical as tiebreaker), capped at depGraphMaxHubs.
func filterAndSortHubs(fanIn map[string]int) []depHub {
	var hubs []depHub
	for imp, count := range fanIn {
		if count < depGraphMinFanIn {
			continue
		}
		hubs = append(hubs, depHub{imp: imp, count: count})
	}

	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].count != hubs[j].count {
			return hubs[i].count > hubs[j].count
		}
		return hubs[i].imp < hubs[j].imp
	})

	if len(hubs) > depGraphMaxHubs {
		hubs = hubs[:depGraphMaxHubs]
	}
	return hubs
}

// renderDepsSection formats the hub packages into a compact prompt section.
func renderDepsSection(hubs []depHub, modulePath string) string {
	var sb strings.Builder
	sb.WriteString("\n\n## Package dependencies\n")
	sb.WriteString("Internal package import graph - most depended-upon packages by fan-in. " +
		"Edits to high fan-in packages have broad blast radius.\n")

	for _, h := range hubs {
		display := h.imp
		if strings.HasPrefix(display, modulePath+"/") {
			display = strings.TrimPrefix(display, modulePath+"/")
		} else if display == modulePath {
			display = "(root)"
		}
		sb.WriteString(fmt.Sprintf("  %s (\u2190%d)\n", display, h.count))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// readModulePath extracts the Go module path from go.mod. Returns empty string
// if go.mod doesn't exist or has no module directive.
func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// parsePackageImports extracts all import paths from non-test Go files in a
// directory. Returns a deduplicated slice of import paths.
func parsePackageImports(dir string) []string {
	fset := token.NewFileSet()
	pkgs, _ := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		name := fi.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.ImportsOnly)

	seen := make(map[string]bool)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, imp := range file.Imports {
				if imp.Path == nil {
					continue
				}
				// import.Path.Value includes surrounding quotes; strip them.
				path := strings.Trim(imp.Path.Value, `"`)
				if path != "" {
					seen[path] = true
				}
			}
		}
	}

	if len(seen) == 0 {
		return nil
	}

	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
