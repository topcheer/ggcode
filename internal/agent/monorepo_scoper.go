package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type monorepoScoperState struct {
	mu          sync.Mutex
	enabled     bool
	rootDir     string
	packages    []string
	touchedDirs map[string]int
	fired       bool
	crossPkg    map[string]bool

	suppressions int
}

var monorepoMarkerFiles = []string{
	"pnpm-workspace.yaml",
	"lerna.json",
	"nx.json",
	"turbo.json",
	"rush.json",
}

var crossCuttingDirNames = map[string]bool{
	"shared": true, "common": true, "lib": true, "libs": true,
	"utils": true, "util": true, "types": true, "config": true,
	"internal": true, "pkg": true, "public": true, "assets": true,
	"docs": true, "scripts": true, "tools": true, "vendor": true,
	"third_party": true, "test": true, "tests": true, "e2e": true,
}

func newMonorepoScoperState() *monorepoScoperState {
	s := &monorepoScoperState{
		touchedDirs: make(map[string]int),
		crossPkg:    make(map[string]bool),
	}
	return s
}

func (s *monorepoScoperState) detectMonorepo(rootDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled || rootDir == "" {
		return
	}

	for _, marker := range monorepoMarkerFiles {
		if _, err := os.Stat(filepath.Join(rootDir, marker)); err == nil {
			s.enabled = true
			s.rootDir = rootDir
			s.discoverPackages(rootDir)
			return
		}
	}

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

func hasPackageManifest(dir string) bool {
	manifests := []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "setup.py", "pom.xml", "build.gradle", "build.gradle.kts"}
	for _, m := range manifests {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

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

func (s *monorepoScoperState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchedDirs = make(map[string]int)
	s.fired = false
	s.suppressions = 0
}

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

func (s *monorepoScoperState) classifyPackage(filePath string) string {

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

	if crossCuttingDirNames[topDir] {
		s.crossPkg[topDir] = true
		return ""
	}

	return topDir
}

const maxMonorepoSuppressions = 3

func (s *monorepoScoperState) maybeWarnScopeSprawl() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled || s.fired || len(s.touchedDirs) < 3 {
		return ""
	}

	var pkgs []string
	for pkg := range s.touchedDirs {
		pkgs = append(pkgs, pkg)
	}

	s.fired = true
	return formatScopeSprawlHint(pkgs)
}

func (s *monorepoScoperState) markUndelivered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppressions >= maxMonorepoSuppressions {

		return
	}
	s.suppressions++
	s.fired = false
}

func formatScopeSprawlHint(pkgs []string) string {

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
