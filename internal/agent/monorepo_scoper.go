package agent

// Monorepo Package Scoping Intelligence
//
// Trend: Claude Code, Cursor, OpenHands, and Devin all implement some form of
// workspace-scoped operations. In monorepos with many packages/services, agents
// frequently operate across the entire workspace when the task is actually
// scoped to one or two packages. This wastes context and can introduce
// cross-package side effects.
//
// What competitors do:
//   - Cursor: workspace-aware search that respects .cursorignore and scopes to
//     the active workspace folder
//   - Claude Code: --cwd flag + CLAUDE.md per-directory for package-scoped context
//   - OpenHands: workspace mount with optional subdirectory scoping
//   - Nx/Turbo: task pipeline graph that knows package boundaries
//
// Gap in ggcode: the agent has monorepo *detection* (project_profile.go marks
// "monorepo" framework) but no *scoping intelligence*. When an agent edits files
// spread across many packages without clear cross-package intent, it likely
// hasn't recognized the package boundary and is operating in "whole-repo" mode.
// This module detects that pattern and injects a concise hint to scope operations
// to the relevant package(s).
//
// Design:
//   - Detects monorepo structure from workspace markers (pnpm-workspace.yaml,
//     lerna.json, nx.json, turbo.json, multiple go.mod/package.json in subdirs)
//   - Tracks which top-level package directories the agent touches via edit tools
//   - When the agent edits files across 3+ distinct package dirs without an
//     explicit cross-package pattern (e.g., shared/common/lib), injects a hint
//     to confirm scope and consider scoping to fewer packages
//   - Fires at most once per run (advisory, non-blocking)
//   - Zero LLM cost - pure filesystem heuristic

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// monorepoScoperState tracks package-level edit scope across a run.
type monorepoScoperState struct {
	mu          sync.Mutex
	enabled     bool            // true if workspace is detected as a monorepo
	rootDir     string          // monorepo root directory
	packages    []string        // discovered top-level package directories
	touchedDirs map[string]int  // package_dir -> edit count
	fired       bool            // whether the hint has already fired this run
	crossPkg    map[string]bool // packages explicitly flagged as cross-cutting (shared, common, lib)

	// suppressions counts how many times a fired hint was bounced by the
	// guidance budget (#687). markUndelivered re-arms the one-shot, but an
	// unbounded retry loop kept re-paying O(touchedDirs) formatting on every
	// iteration during a saturated turn — and errorCompound, which always runs
	// before the monorepo check, permanently starved it. After
	// maxMonorepoSuppressions consecutive rejections we give up for the run.
	suppressions int
}

// monorepoMarkers are files whose presence at a directory level indicates a
// monorepo root.
var monorepoMarkerFiles = []string{
	"pnpm-workspace.yaml",
	"lerna.json",
	"nx.json",
	"turbo.json",
	"rush.json",
}

// crossCuttingDirNames are directory names that are legitimately shared across
// packages and should not count toward "package sprawl" detection.
var crossCuttingDirNames = map[string]bool{
	"shared": true, "common": true, "lib": true, "libs": true,
	"utils": true, "util": true, "types": true, "config": true,
	"internal": true, "pkg": true, "public": true, "assets": true,
	"docs": true, "scripts": true, "tools": true, "vendor": true,
	"third_party": true, "test": true, "tests": true, "e2e": true,
}

// newMonorepoScoperState creates a new scoper, auto-detecting monorepo structure.
func newMonorepoScoperState() *monorepoScoperState {
	s := &monorepoScoperState{
		touchedDirs: make(map[string]int),
		crossPkg:    make(map[string]bool),
	}
	return s
}

// detectMonorepo scans the workspace root for monorepo markers and discovers
// top-level package directories. Called lazily on first edit.
func (s *monorepoScoperState) detectMonorepo(rootDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled || rootDir == "" {
		return
	}

	// Check for explicit monorepo marker files at root.
	for _, marker := range monorepoMarkerFiles {
		if _, err := os.Stat(filepath.Join(rootDir, marker)); err == nil {
			s.enabled = true
			s.rootDir = rootDir
			s.discoverPackages(rootDir)
			return
		}
	}

	// Fallback: count package manifests in immediate subdirectories.
	// If there are 3+ subdirs with their own package.json or go.mod,
	// treat as a monorepo.
	pkgCount := 0
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		subdir := filepath.Join(rootDir, entry.Name())
		if hasPackageManifest(subdir) {
			pkgCount++
			s.packages = append(s.packages, entry.Name())
		}
	}
	if pkgCount >= 3 {
		s.enabled = true
		s.rootDir = rootDir
	}
}

// hasPackageManifest checks if a directory contains a package manifest file.
func hasPackageManifest(dir string) bool {
	manifests := []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "setup.py", "pom.xml", "build.gradle", "build.gradle.kts"}
	for _, m := range manifests {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// discoverPackages finds package directories from pnpm-workspace.yaml globs
// or by scanning immediate subdirectories.
func (s *monorepoScoperState) discoverPackages(rootDir string) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if hasPackageManifest(filepath.Join(rootDir, entry.Name())) {
			s.packages = append(s.packages, entry.Name())
		}
	}
}

// reset clears per-run state. #687: the suppression budget is per-run too —
// a saturated turn must not permanently disarm the detector for later runs.
func (s *monorepoScoperState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchedDirs = make(map[string]int)
	s.fired = false
	s.suppressions = 0
}

// recordEdit tracks which package directory a file edit targets.
func (s *monorepoScoperState) recordEdit(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled || filePath == "" {
		return
	}

	pkgDir := s.classifyPackage(filePath)
	if pkgDir == "" {
		return
	}
	s.touchedDirs[pkgDir]++
}

// classifyPackage determines which top-level package a file path belongs to.
// Returns "" for files outside any known package or in cross-cutting dirs.
func (s *monorepoScoperState) classifyPackage(filePath string) string {
	// Normalize separators first: rootDir comes from filepath.Join (backslashes
	// on Windows) while filePath is usually the LLM's literal tool argument
	// (forward slashes). Splitting the raw strings on filepath.Separator made
	// every prefix/rel test fail on Windows, silently disabling the whole
	// detector there (both separator styles happen to agree on Linux, which is
	// why this only shows up in Windows verification).
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	rootDir := ""
	if s.rootDir != "" {
		rootDir = strings.ReplaceAll(s.rootDir, "\\", "/")
	}

	rel := filePath
	if rootDir != "" && strings.HasPrefix(filePath, rootDir) {
		rel = strings.TrimPrefix(filePath, rootDir)
		rel = strings.TrimPrefix(rel, "/")
	}

	parts := strings.SplitN(rel, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		return ""
	}
	topDir := parts[0]

	// Cross-cutting directories are not counted as packages.
	if crossCuttingDirNames[topDir] {
		s.crossPkg[topDir] = true
		return ""
	}

	return topDir
}

// maxMonorepoSuppressions caps how many budget rejections the sprawl hint
// tolerates before giving up for the run (#687). Three re-arms is enough to
// survive transient saturation without an unbounded per-iteration retry.
const maxMonorepoSuppressions = 3

// maybeWarnScopeSprawl checks if the agent is editing across too many packages
// without apparent cross-package intent. Returns a hint message or "".
// Termination is guaranteed by `fired` alone: markUndelivered re-arms it at
// most maxMonorepoSuppressions times, after which it stops re-arming and
// fired stays true for the rest of the run (#695: the old
// `suppressions > max` clause here was unreachable dead code — suppressions
// can never exceed maxMonorepoSuppressions).
func (s *monorepoScoperState) maybeWarnScopeSprawl() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled || s.fired || len(s.touchedDirs) < 3 {
		return ""
	}

	// Build a summary of packages touched.
	var pkgs []string
	for pkg := range s.touchedDirs {
		pkgs = append(pkgs, pkg)
	}

	s.fired = true
	return formatScopeSprawlHint(pkgs)
}

// markUndelivered un-burns the one-shot chance consumed by a
// maybeWarnScopeSprawl call whose guidance was suppressed by the
// per-turn guidance budget (#681: one-shot + budget-droppable = the
// detector randomly goes dark for the whole run). After this call the
// sprawl check may fire again on a later, less saturated iteration.
// #687: the re-arm is bounded — a permanently saturated turn (e.g. an
// errorCompound storm that always claims the budget first) would otherwise
// retry forever, re-paying the O(touchedDirs) format cost each iteration.
func (s *monorepoScoperState) markUndelivered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppressions >= maxMonorepoSuppressions {
		// Re-arm budget exhausted (#687): fired stays true, which is what
		// stops the retry cycle — every later maybeWarnScopeSprawl call
		// short-circuits on it (#695: the old comment claimed an
		// unreachable `> cap` guard in maybeWarnScopeSprawl did this).
		return
	}
	s.suppressions++
	s.fired = false
}

// formatScopeSprawlHint produces the concise hint message.
func formatScopeSprawlHint(pkgs []string) string {
	// Cap displayed package names.
	display := pkgs
	if len(display) > 5 {
		display = display[:5]
	}
	pkgList := strings.Join(display, ", ")
	if len(pkgs) > 5 {
		pkgList += ", ..."
	}
	return "[monorepo-scope] Editing across " + strconv.Itoa(len(pkgs)) + " packages (" +
		pkgList + "). If this task is scoped to fewer packages, consider using " +
		"package-specific paths and build/test commands to avoid cross-package " +
		"side effects. Confirm the scope is intentional."
}
