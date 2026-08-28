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
	"fmt"
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
	goModPath := filepath.Join(workingDir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			result := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			return result
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
	combined, err := cmd.CombinedOutput()
	if err != nil {
		debug.Log("test-impact", "go list failed in %s: %v", workingDir, err)
		return nil, ""
	}

	modPath := goModulePath(workingDir)
	if modPath == "" {
		// Don't return early, continue with graph building
	}

	graph := make(map[string][]string)
	for _, line := range strings.Split(string(combined), "\n") {
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
// slash-separated) within the same module that transitively import any of the
// given changed package directories. This expands the test scope beyond just
// the changed packages to include their downstream consumers — the core of
// transitive test impact analysis.
//
// The function uses a BFS closure: starting from changed packages, it repeatedly
// adds all module-internal packages that import anything in the current target
// set until a fixpoint is reached. This ensures that multi-level dependency chains
// are fully traversed (e.g., A→B→C where C changed: A imports B, B imports C → A
// is included).
//
// For example, if internal/util changed, internal/safego imports util, and
// internal/agent imports safego (but not util directly), then internal/agent's
// tests should also run because they are affected by the change to util via
// the intermediate dependency.
//
// Returns nil if the import graph can't be built or no importers are found.
func transitiveImporters(workingDir string, changedDirs []string) []string {
	graph, modPath := buildImportGraph(workingDir)
	if graph == nil || len(changedDirs) == 0 {
		return nil
	}

	// Build initial target set: modPath/dir for each changed dir.
	targets := make(map[string]bool, len(changedDirs))
	for _, d := range changedDirs {
		targetPath := modPath + "/" + filepath.ToSlash(d)
		targets[targetPath] = true
	}
	debug.Log("test-impact", "transitiveImporters: initial targets=%v", targets)

	// BFS closure: repeatedly add importers of current targets until fixpoint.
	seen := make(map[string]bool)
	iteration := 0
	for len(targets) > 0 {
		iteration++
		debug.Log("test-impact", "transitiveImporters: iteration %d, targets=%d", iteration, len(targets))
		// Find all packages that import any target in the current set.
		newTargets := make(map[string]bool)
		for pkgPath, imports := range graph {
			// Skip if already in the transitive set.
			if seen[pkgPath] {
				continue
			}
			// Skip packages that are already in targets (changed packages or previously added).
			if targets[pkgPath] {
				continue
			}
			// Check if this package imports any target.
			for _, imp := range imports {
				if targets[imp] {
					debug.Log("test-impact", "transitiveImporters: found importer %s imports %s", pkgPath, imp)
					newTargets[pkgPath] = true
					// Add to seen (all packages in the graph are in the module).
					seen[pkgPath] = true
					debug.Log("test-impact", "transitiveImporters: marked seen %s", pkgPath)
					break
				}
			}
		}
		debug.Log("test-impact", "transitiveImporters: iteration %d found %d new importers", iteration, len(newTargets))
		// Add newly discovered importers to targets for the next iteration.
		for pkgPath := range newTargets {
			targets[pkgPath] = true
		}
		// If no new importers were found, we've reached fixpoint.
		if len(newTargets) == 0 {
			break
		}
	}
	debug.Log("test-impact", "transitiveImporters: BFS complete, seen=%d packages", len(seen))

	if len(seen) == 0 {
		return nil
	}

	// Collect importers in sorted order for determinism.
	importers := make([]string, 0, len(seen))
	for pkgPath := range seen {
		relDir := strings.TrimPrefix(pkgPath, modPath+"/")
		// Ensure the package is in the module (relDir should be different from pkgPath
		// unless the module path is empty or the package is outside the module).
		if relDir != pkgPath && relDir != "" {
			importers = append(importers, filepath.ToSlash(relDir))
		} else {
			debug.Log("test-impact", "transitiveImporters: skipping %s (relDir=%s)", pkgPath, relDir)
		}
	}
	sort.Strings(importers)
	debug.Log("test-impact", "transitiveImporters: result=%v", importers)
	return importers
}

// impactScopedTestCommandWithDeps builds a `go test` command that covers all
// changed Go packages AND their transitive importers (downstream consumers).
// This is the enhanced version of impactScopedTestCommand — it uses the import
// graph to expand the test scope, ensuring that packages affected by the change
// (not just the changed packages themselves) have their tests run.
//
// The function preserves all changed directories unconditionally; importer
// directories fill the remaining slots up to a 20-package cap. If truncation
// occurs, the suffix indicates how many importers were omitted (e.g., "# +5
// importers omitted").
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

	// Build changedSet for quick lookup.
	changedSet := make(map[string]bool, len(changedDirs))
	for _, d := range changedDirs {
		changedSet[d] = true
	}

	// Convert allDirs to a sorted list for deterministic output.
	dirList := make([]string, 0, len(allDirs))
	for d := range allDirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)

	// Separate changed dirs and importers to preserve changed dirs unconditionally.
	// Cap at 20 packages: changed dirs are always included, importers fill remaining slots.
	var changedInList, importersInList []string
	for _, d := range dirList {
		if changedSet[d] {
			changedInList = append(changedInList, d)
		} else {
			importersInList = append(importersInList, d)
		}
	}

	// Build final list: always include all changed dirs, then add importers up to cap.
	finalList := make([]string, 0, len(changedInList))
	finalList = append(finalList, changedInList...)

	remainingCap := 20 - len(changedInList)
	var omittedImporters int
	if remainingCap > 0 && len(importersInList) > 0 {
		// Importers are already sorted from the earlier sort.Strings(dirList).
		if len(importersInList) > remainingCap {
			finalList = append(finalList, importersInList[:remainingCap]...)
			omittedImporters = len(importersInList) - remainingCap
		} else {
			finalList = append(finalList, importersInList...)
		}
	} else if len(importersInList) > 0 {
		// No cap remaining but we have importers.
		omittedImporters = len(importersInList)
	}

	parts := make([]string, len(finalList))
	for i, d := range finalList {
		parts[i] = "./" + d + "/"
	}
	cmd := "go test " + strings.Join(parts, " ")
	if omittedImporters > 0 {
		cmd += fmt.Sprintf(" # +%d importers omitted", omittedImporters)
	} else if len(allDirs) > 20 {
		// Fallback for edge cases: all changed, no importers, but still truncated.
		cmd += " # +more"
	}

	if len(importerDirs) > 0 {
		debug.Log("test-impact", "impact command with deps: %d changed + %d importers = %d total packages",
			len(changedDirs), len(importerDirs), len(allDirs))
	}
	return cmd
}
