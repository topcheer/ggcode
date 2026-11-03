package agent

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	hubPackageThreshold = 5

	hubPackageFanInBudget = 200 * time.Millisecond
)

var hubSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"out": true, "target": true, "__pycache__": true, "coverage": true,
	"bin": true, ".git": true, ".vscode": true, ".idea": true,
	"testdata": true, "test_data": true, "mocks": true, "fixtures": true,
}

type hubPackageState struct {
	fanIn       map[string]int
	modulePath  string
	checked     map[string]bool
	initialized bool
}

func newHubPackageState() *hubPackageState {
	return &hubPackageState{
		fanIn:   make(map[string]int),
		checked: make(map[string]bool),
	}
}

func (s *hubPackageState) reset() {
	s.checked = make(map[string]bool)

}

func (s *hubPackageState) ensureFanIn(workingDir string) {
	if s.initialized {
		return
	}

	s.modulePath = hubReadModulePath(workingDir)
	if s.modulePath == "" {
		return
	}
	s.initialized = true

	start := time.Now()
	s.fanIn = hubComputeFanIn(workingDir, s.modulePath)
	debug.Log("hub-pkg-guard", "fan-in map computed: %d packages in %v", len(s.fanIn), time.Since(start))
}

func (a *Agent) checkHubPackage(filePath string) string {
	if a.hubPackageGuard == nil || filePath == "" {
		return ""
	}

	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	abs := filePath
	if !filepath.IsAbs(abs) && a.workingDir != "" {
		abs = filepath.Join(a.workingDir, filePath)
	}
	if a.hubPackageGuard.checked[abs] {
		return ""
	}

	a.hubPackageGuard.ensureFanIn(a.workingDir)
	if len(a.hubPackageGuard.fanIn) == 0 {
		return ""
	}

	pkgPath := hubFileToImportPath(a.workingDir, filePath, a.hubPackageGuard.modulePath)
	if pkgPath == "" {
		return ""
	}
	a.hubPackageGuard.checked[abs] = true

	count := a.hubPackageGuard.fanIn[pkgPath]
	if count < hubPackageThreshold {
		return ""
	}

	display := pkgPath
	if strings.HasPrefix(display, a.hubPackageGuard.modulePath+"/") {
		display = strings.TrimPrefix(display, a.hubPackageGuard.modulePath+"/")
	}

	debug.Log("hub-pkg-guard", "hub package %s (←%d) edited: %s", display, count, filepath.Base(filePath))
	return fmt.Sprintf("Impact awareness: package %q is imported by %d internal packages. "+
		"Consider running lsp_references on any changed symbols to verify downstream compatibility.",
		display, count)
}

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

func hubFileToImportPath(workingDir, filePath, modulePath string) string {
	if modulePath == "" {
		return ""
	}
	abs := filePath
	if !filepath.IsAbs(abs) && workingDir != "" {
		abs = filepath.Join(workingDir, filePath)
	}
	dir := filepath.Dir(abs)

	modRoot, modName, ok := hubNearestGoMod(dir, workingDir)

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

func hubNearestGoMod(dir, root string) (modRoot, modName string, ok bool) {
	for cur := dir; ; {
		modPath := filepath.Join(cur, "go.mod")
		if _, statErr := os.Stat(modPath); statErr == nil {
			data, err := os.ReadFile(modPath)
			if err != nil {

				debug.Log("hub-pkg-guard", "go.mod at %s unreadable: %v", cur, err)
				return "", "", false
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return cur, strings.TrimSpace(strings.TrimPrefix(line, "module ")), true
				}
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {

			debug.Log("hub-pkg-guard", "go.mod stat indeterminate at %s: %v", cur, statErr)
			return "", "", false
		}
		if cur == root || cur == filepath.Dir(cur) {
			return "", "", true
		}
		cur = filepath.Dir(cur)
	}
}

func hubComputeFanIn(root, modulePath string) map[string]int {
	fanIn := make(map[string]int)

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

func hubIsInternal(imp, modulePath string) bool {
	return strings.HasPrefix(imp, modulePath+"/") || imp == modulePath
}
