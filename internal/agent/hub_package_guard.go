package agent

// hub_package_guard.go implements Hub Package Edit Awareness - a per-edit
// contextual guard that informs the agent when it edits a file in a high
// fan-in (widely-imported) internal package.
//
// Research basis: GraphRAG (Edge et al. 2024) and RepoHyper (Jin et al. 2024)
// demonstrate that dependency-graph-augmented context improves navigation and
// edit accuracy. ggcode already injects a static package dependency section
// into the system prompt (agentruntime/package_deps.go), but that information
// is static and "far" from the edit action - the model must mentally connect
// "I am editing internal/config" with "config is a hub package." This guard
// fires contextually at edit time, surfacing the exact importer count for the
// specific package being modified, prompting the agent to proactively check
// downstream callers.
//
// Competitor mapping:
//   - Cursor: shows reference counts inline but only on explicit hover
//   - Claude Code: no per-edit blast-radius awareness
//   - GitHub Copilot: no import-graph awareness during edits
//   - Cline/OpenHands: no dependency-graph-based edit guidance
//
// Relationship to existing guards:
//   - package_deps.go (system prompt): static, top-10 hubs, session-level
//   - export_guard.go: fires AFTER breaking changes are detected (reactive)
//   - THIS guard: fires for ANY edit to a hub package (proactive awareness)
//
// Design:
//   - Zero LLM cost (pure Go AST parsing, cached per session)
//   - Time-budgeted (200ms cap for initial computation)
//   - Lazy initialization (computed on first Go file edit)
//   - Fires once per file per run to avoid noise
//   - High threshold (≥5 importers) — below this, blast radius is limited
//   - Complements export_guard: provides scale context even for non-breaking edits

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
	// hubPackageThreshold is the minimum fan-in for the guard to fire.
	// Below this, the package has limited downstream impact.
	hubPackageThreshold = 5

	// hubPackageFanInBudget caps the time spent computing the fan-in map.
	hubPackageFanInBudget = 200 * time.Millisecond
)

// hubSkipDirs are directories excluded from the package walk.
var hubSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"out": true, "target": true, "__pycache__": true, "coverage": true,
	"bin": true, ".git": true, ".vscode": true, ".idea": true,
	"testdata": true, "test_data": true, "mocks": true, "fixtures": true,
}

// hubPackageState caches per-session package fan-in data and tracks per-run
// checked files to avoid repeating the same warning on iterative edits.
type hubPackageState struct {
	fanIn       map[string]int  // package import path → number of importing packages
	modulePath  string          // Go module path from go.mod
	checked     map[string]bool // absolute file paths already warned this run
	initialized bool            // whether fanIn has been computed
}

func newHubPackageState() *hubPackageState {
	return &hubPackageState{
		fanIn:   make(map[string]int),
		checked: make(map[string]bool),
	}
}

func (s *hubPackageState) reset() {
	s.checked = make(map[string]bool)
	// Keep fanIn and modulePath — they're session-level cache, expensive to recompute
}

// ensureFanIn computes the full internal package fan-in map if not already done.
// Safe to call multiple times; computes only once per session.
func (s *hubPackageState) ensureFanIn(workingDir string) {
	if s.initialized {
		return
	}

	// #1466-B: initialized is set only AFTER a successful computation - the
	// old pre-set meant a go.work-only workspace (no root go.mod; the
	// standard monorepo layout) or any transient lookup failure disabled
	// the guard for the Agent's ENTIRE lifetime with no retry path (reset
	// deliberately preserves initialized).
	s.modulePath = hubReadModulePath(workingDir)
	if s.modulePath == "" {
		return // not initialized - retried on the next call
	}
	s.initialized = true

	start := time.Now()
	s.fanIn = hubComputeFanIn(workingDir, s.modulePath)
	debug.Log("hub-pkg-guard", "fan-in map computed: %d packages in %v", len(s.fanIn), time.Since(start))
}

// checkHubPackage returns a blast-radius awareness hint if the edited file's
// package has high fan-in (≥ hubPackageThreshold importers). Returns "" if not
// applicable, not a Go project, or already warned for this file this run.
func (a *Agent) checkHubPackage(filePath string) string {
	if a.hubPackageGuard == nil || filePath == "" {
		return ""
	}

	// Only check Go source files (not test files — test packages are internal).
	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	// Resolve to absolute path for dedup.
	abs := filePath
	if !filepath.IsAbs(abs) && a.workingDir != "" {
		abs = filepath.Join(a.workingDir, filePath)
	}
	if a.hubPackageGuard.checked[abs] {
		return ""
	}

	// Lazily compute the fan-in map (once per session).
	a.hubPackageGuard.ensureFanIn(a.workingDir)
	if len(a.hubPackageGuard.fanIn) == 0 {
		return ""
	}
	// #1574-C: mark checked only AFTER a successful initialization - the
	// old ordering latched checked before ensureFanIn could fail (e.g. a
	// transient root go.mod miss leaves initialized=false), and that file
	// was never re-examined this run.
	a.hubPackageGuard.checked[abs] = true

	// Determine the edited file's package import path.
	pkgPath := hubFileToImportPath(a.workingDir, filePath, a.hubPackageGuard.modulePath)
	if pkgPath == "" {
		return ""
	}

	count := a.hubPackageGuard.fanIn[pkgPath]
	if count < hubPackageThreshold {
		return ""
	}

	// Render a short display name by trimming the module prefix.
	display := pkgPath
	if strings.HasPrefix(display, a.hubPackageGuard.modulePath+"/") {
		display = strings.TrimPrefix(display, a.hubPackageGuard.modulePath+"/")
	}

	debug.Log("hub-pkg-guard", "hub package %s (←%d) edited: %s", display, count, filepath.Base(filePath))
	return fmt.Sprintf("Impact awareness: package %q is imported by %d internal packages. "+
		"Consider running lsp_references on any changed symbols to verify downstream compatibility.",
		display, count)
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

// hubReadModulePath extracts the Go module path from go.mod.
func hubReadModulePath(workingDir string) string {
	if workingDir == "" {
		return ""
	}
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

// hubFileToImportPath converts a file path to its Go import path relative to
// the module root.
func hubFileToImportPath(workingDir, filePath, modulePath string) string {
	if modulePath == "" {
		return ""
	}
	abs := filePath
	if !filepath.IsAbs(abs) && workingDir != "" {
		abs = filepath.Join(workingDir, filePath)
	}
	dir := filepath.Dir(abs)
	// #1574-A: a NESTED submodule (its own go.mod) keys the fan-in map by
	// ITS module path (its intra-module imports say so) - concatenating
	// the ROOT module path produced github.com/root/sub/... keys that
	// never exist in the map, a guaranteed 0 fan-in for every file under
	// it (live in this very repo: ggcode-relay). Walk up to the nearest
	// go.mod between dir and workingDir.
	modRoot, modName, ok := hubNearestGoMod(dir, workingDir)
	// #1614-B: unreadable go.mod = UNKNOWN key - return empty (no fallback
	// to root keying, no latch-burn) so the next call retries.
	if !ok {
		return ""
	}
	if modRoot != "" && modName != "" && modName != modulePath {
		if relNested, err := filepath.Rel(modRoot, dir); err == nil {
			relNested = filepath.ToSlash(relNested)
			if relNested == "." {
				return modName
			}
			return modName + "/" + relNested
		}
	}
	rel, err := filepath.Rel(workingDir, dir)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return modulePath
	}
	return modulePath + "/" + rel
}

// hubNearestGoMod walks up from dir toward root looking for the nearest
// go.mod, returning its directory and module name ("" when none found).
// hubNearestGoMod walks up from dir toward root. Third state per #1614-B:
// a go.mod EXISTS but cannot be READ (permission, transient IO) returns
// ok=false WITHOUT falling through - the caller must treat the key as
// UNKNOWN (skip the sample/latch this round) rather than silently
// re-keying via the root module (query 0 + latch = wrong key forever
// this run).
func hubNearestGoMod(dir, root string) (modRoot, modName string, ok bool) {
	for cur := dir; ; {
		modPath := filepath.Join(cur, "go.mod")
		if _, statErr := os.Stat(modPath); statErr == nil {
			data, err := os.ReadFile(modPath)
			if err != nil {
				// Exists but unreadable: indeterminate - stop here, report
				// not-ok so callers freeze rather than mis-key.
				debug.Log("hub-pkg-guard", "go.mod at %s unreadable: %v", cur, err)
				return "", "", false
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return cur, strings.TrimSpace(strings.TrimPrefix(line, "module ")), true
				}
			}
		}
		if cur == root || cur == filepath.Dir(cur) {
			return "", "", true // none found anywhere: determinate absence
		}
		cur = filepath.Dir(cur)
	}
}

// hubComputeFanIn walks all Go package directories under root and counts how
// many internal packages import each package. Time-budgeted to avoid blocking
// the agent loop on large monorepos.
func hubComputeFanIn(root, modulePath string) map[string]int {
	fanIn := make(map[string]int)

	// Collect directories containing non-test .go files.
	// #1634: the deadline is set AFTER the walk - set at the entry it
	// charged the 200ms budget against the UNCAPPED directory walk itself
	// (slow disks / huge repos), the counting loop broke on its first
	// check, and the session-cached EMPTY fanIn map permanently silenced
	// the guard (advisory hints never fire). The walk is a one-time
	// session cost by design; the budget bounds the per-package parsing.
	var pkgDirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name != filepath.Base(root) && (name[0] == '.' || hubSkipDirs[name]) {
			return filepath.SkipDir
		}
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				pkgDirs = append(pkgDirs, path)
				break
			}
		}
		return nil
	})

	deadline := time.Now().Add(hubPackageFanInBudget)
	// Parse imports from each package and count edges to internal packages.
	// #1614-A: each package dir is keyed by its NEAREST go.mod module, not
	// just the root - a nested module's intra-module imports carry ITS OWN
	// module prefix (ggcode-relay = github.com/topcheer/ggcode-relay, not a
	// root prefix), so the root-only check classified them external and the
	// map never held the keys the (corrected) query side looks up: the
	// #1574 fix was a no-op for the live case. Same-class fix at the SOURCE.
	modOf := make(map[string]string)
	for _, dir := range pkgDirs {
		if time.Now().After(deadline) {
			debug.Log("hub-pkg-guard", "fan-in computation: time budget hit after %d packages", len(pkgDirs))
			break
		}
		mod := modOf[dir]
		if mod == "" {
			if _, m, ok := hubNearestGoMod(dir, root); ok && m != "" {
				mod = m
			} else if !ok {
				// Indeterminate: skip this package this round rather than
				// counting it under a wrong module key.
				continue
			} else {
				mod = modulePath
			}
			modOf[dir] = mod
		}
		for _, imp := range hubParseImports(dir) {
			if hubIsInternal(imp, mod) {
				fanIn[imp]++
			}
		}
	}

	return fanIn
}

// hubParseImports extracts deduplicated import paths from non-test .go files
// in a directory.
func hubParseImports(dir string) []string {
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

// hubIsInternal reports whether an import path belongs to the module.
func hubIsInternal(imp, modulePath string) bool {
	return strings.HasPrefix(imp, modulePath+"/") || imp == modulePath
}
