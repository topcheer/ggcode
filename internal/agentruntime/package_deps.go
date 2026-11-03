package agentruntime

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
	depGraphMaxHubs    = 10
	depGraphTimeBudget = 200 * time.Millisecond
	depGraphMinFanIn   = 2
)

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

type depHub struct {
	imp   string
	count int
}

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
				continue
			}
			if _, ok := importToDir[imp]; ok {
				fanIn[imp]++
			}
		}
	}

	return fanIn
}

func isInternalImport(imp, modulePath string) bool {
	return strings.HasPrefix(imp, modulePath+"/") || imp == modulePath
}

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

func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)

		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
			mod := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "module"))

			if v, _, ok := strings.Cut(mod, "//"); ok {
				mod = v
			}
			return strings.TrimSpace(mod)
		}
	}
	return ""
}

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
