package agent

// test_impact_deps.go implements import-graph-based transitive test impact
// analysis. When a package changes, not only should its own tests run, but
// also the tests of packages that import it — because those packages' behavior
// may be affected by the change.
//
// This mirrors what JetBrains AI Assistant does: "identifies affected tests
// from code changes" by traversing the dependency graph. Cursor similarly
// analyzes which tests are affected by tracking import relationships.
//
// Implementation:
//   - Uses `go list` to build the module's import graph (cached for 5 min)
//   - Reverses the graph to find downstream consumers of changed packages
//   - Expands the scoped test command to include those consumers
//   - Falls back silently if `go list` fails (e.g., build tag issues)
//
// Performance: `go list ./...` takes ~1-3s on first call for a medium module.
// The result is cached for 5 minutes, so subsequent hints are instant. A 15s
// timeout prevents blocking the agent loop. If it fails or times out, the
// system falls back to the existing package-level TIA (test_impact.go).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const importGraphTTL = 5 * time.Minute

// importGraphEntry caches the result of `go list` to avoid repeated slow calls.
type importGraphEntry struct {
	graph   map[string][]string // importPath -> imported paths
	modPath string              // module path from go.mod
	builtAt time.Time
}

var importGraphCache struct {
	sync.Mutex
	dir      string
	data     importGraphEntry
	building bool
}

// goModulePath reads go.mod and extracts the module import path.
// Returns "" if go.mod doesn't exist or can't be parsed.
func goModulePath(workingDir string) string {
	data, err := os.ReadFile(filepath.Join(workingDir, "go.mod"))
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

// detectGoBuildTags scans the Makefile for Go build tags (e.g., -tags goolm).
// This ensures `go list` sees all packages, including those behind build
// constraints. Returns nil if no tags are found.
func detectGoBuildTags(workingDir string) []string {
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		data, err := os.ReadFile(filepath.Join(workingDir, mf))
		if err != nil {
			continue
		}
		content := string(data)
		words := strings.Fields(content)
		for i, w := range words {
			// Match -tags <tags> (space-separated)
			if w == "-tags" && i+1 < len(words) {
				tag := strings.Trim(words[i+1], "'\"")
				tags := strings.Split(tag, ",")
				var result []string
				for _, t := range tags {
					t = strings.TrimSpace(t)
					if t != "" {
						result = append(result, t)
					}
				}
				return result
			}
			// Match -tags=<tags>
			if strings.HasPrefix(w, "-tags=") {
				tag := strings.TrimPrefix(w, "-tags=")
				tag = strings.Trim(tag, "'\"")
				tags := strings.Split(tag, ",")
				var result []string
				for _, t := range tags {
					t = strings.TrimSpace(t)
					if t != "" {
						result = append(result, t)
					}
				}
				return result
			}
		}
		break // found Makefile but no tags — stop searching
	}
	return nil
}

// buildImportGraph runs `go list` to build a map of package import paths to
// their directly imported packages. The result is cached for importGraphTTL
// to avoid repeated slow calls. Returns nil if the command fails, times out,
// or the directory is not a Go module.
func buildImportGraph(workingDir string) (map[string][]string, string) {
	if workingDir == "" {
		return nil, ""
	}

	importGraphCache.Lock()
	defer importGraphCache.Unlock()

	// Return cached result if valid.
	if importGraphCache.dir == workingDir &&
		time.Since(importGraphCache.data.builtAt) < importGraphTTL &&
		importGraphCache.data.graph != nil {
		return importGraphCache.data.graph, importGraphCache.data.modPath
	}

	// If no cache exists yet, do NOT block the caller — return nil and
	// trigger a background build. The next call (after the background build
	// completes) will have the cached graph. This prevents the verify hint
	// path from stalling on `go list` for 1-3 seconds on first invocation.
	if importGraphCache.data.graph == nil || importGraphCache.dir != workingDir {
		// Kick off background build (deduplicated via building flag).
		if !importGraphCache.building {
			importGraphCache.building = true
			importGraphCache.dir = workingDir
			go func() {
				graph, modPath := runGoList(workingDir)
				importGraphCache.Lock()
				importGraphCache.building = false
				if graph != nil {
					importGraphCache.data = importGraphEntry{
						graph:   graph,
						modPath: modPath,
						builtAt: time.Now(),
					}
					debug.Log("test-impact", "import graph built in background: %d packages in %s", len(graph), workingDir)
				}
				importGraphCache.Unlock()
			}()
		}
		return nil, ""
	}

	// Cache exists but is stale — return stale data immediately and refresh
	// in the background on next tick. This avoids blocking on every TTL expiry.
	return importGraphCache.data.graph, importGraphCache.data.modPath
}

// runGoList executes `go list ./...` and builds the import graph.
// Returns nil if the command fails or times out.
func runGoList(workingDir string) (map[string][]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := []string{"list", "-f", "{{.ImportPath}}\x01{{join .Imports \",\"}}"}
	if tags := detectGoBuildTags(workingDir); len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "./...")

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = workingDir
	out, err := cmd.Output()
	if err != nil {
		debug.Log("test-impact", "go list failed in %s: %v", workingDir, err)
		return nil, ""
	}

	modPath := goModulePath(workingDir)
	if modPath == "" {
		return nil, ""
	}

	graph := make(map[string][]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x01", 2)
		if len(parts) < 1 {
			continue
		}
		importPath := parts[0]
		if len(parts) == 2 && parts[1] != "" {
			graph[importPath] = strings.Split(parts[1], ",")
		} else {
			graph[importPath] = nil
		}
	}
	return graph, modPath
}

// transitiveImporters returns package directories (relative to workingDir,
// slash-separated) within the same module that directly import any of the
// given changed package directories. This expands the test scope beyond just
// the changed packages to include their downstream consumers — the core of
// transitive test impact analysis.
//
// For example, if internal/agent changed and internal/chat imports it, then
// internal/chat's tests should also run because they exercise the changed code.
//
// Returns nil if the import graph can't be built or no importers are found.
func transitiveImporters(workingDir string, changedDirs []string) []string {
	graph, modPath := buildImportGraph(workingDir)
	if graph == nil || modPath == "" || len(changedDirs) == 0 {
		return nil
	}

	// Build target set: modPath/dir for each changed dir.
	targets := make(map[string]bool, len(changedDirs))
	for _, d := range changedDirs {
		targets[modPath+"/"+filepath.ToSlash(d)] = true
	}

	// Scan all packages to find direct importers of any target.
	seen := make(map[string]bool)
	var importers []string
	for pkgPath, imports := range graph {
		for _, imp := range imports {
			if targets[imp] && !seen[pkgPath] {
				seen[pkgPath] = true
				// Convert import path to relative directory.
				relDir := strings.TrimPrefix(pkgPath, modPath+"/")
				if relDir != pkgPath { // ensure it's in the module
					importers = append(importers, filepath.ToSlash(relDir))
				}
				break
			}
		}
	}

	if len(importers) == 0 {
		return nil
	}
	sort.Strings(importers)
	return importers
}

// impactScopedTestCommandWithDeps builds a `go test` command that covers all
// changed Go packages AND their direct importers (downstream consumers).
// This is the enhanced version of impactScopedTestCommand — it uses the import
// graph to expand the test scope, ensuring that packages affected by the change
// (not just the changed packages themselves) have their tests run.
//
// Falls back to impactScopedTestCommand if the import graph can't be built.
func impactScopedTestCommandWithDeps(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	if !fileExists(filepath.Join(workingDir, "go.mod")) {
		return ""
	}

	changedDirs := changedGoPackageDirs(workingDir)
	if len(changedDirs) == 0 {
		return ""
	}

	// Find downstream consumers.
	importerDirs := transitiveImporters(workingDir, changedDirs)

	// Merge changed dirs and importer dirs, deduplicating.
	allDirs := make(map[string]bool, len(changedDirs)+len(importerDirs))
	for _, d := range changedDirs {
		allDirs[d] = true
	}
	for _, d := range importerDirs {
		allDirs[d] = true
	}

	if len(allDirs) == 0 {
		return impactScopedTestCommand(workingDir)
	}

	// Cap at 20 packages to avoid generating an unwieldy command line.
	dirList := make([]string, 0, len(allDirs))
	for d := range allDirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	capped := false
	if len(dirList) > 20 {
		dirList = dirList[:20]
		capped = true
	}

	parts := make([]string, len(dirList))
	for i, d := range dirList {
		parts[i] = "./" + d + "/"
	}
	cmd := "go test " + strings.Join(parts, " ")
	if capped {
		cmd += " # +more"
	}

	if len(importerDirs) > 0 {
		debug.Log("test-impact", "impact command with deps: %d changed + %d importers = %d total packages",
			len(changedDirs), len(importerDirs), len(allDirs))
	}
	return cmd
}
