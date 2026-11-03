package agent

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
	"github.com/topcheer/ggcode/internal/safego"
)

const importGraphTTL = 5 * time.Minute

type importGraphEntry struct {
	graph   map[string][]string
	modPath string
	builtAt time.Time
}

var importGraphCache struct {
	sync.Mutex
	dir      string
	data     importGraphEntry
	building bool
}

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

func detectGoBuildTags(workingDir string) []string {
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		data, err := os.ReadFile(filepath.Join(workingDir, mf))
		if err != nil {
			continue
		}
		content := string(data)

		vars := makefileVars(content)
		words := strings.Fields(expandMakefileVars(content, vars))
		for i, w := range words {

			if w == "-tags" && i+1 < len(words) {
				return splitBuildTagList(words[i+1])
			}

			if strings.HasPrefix(w, "-tags=") {
				return splitBuildTagList(strings.TrimPrefix(w, "-tags="))
			}
		}
		break
	}
	return nil
}

func makefileVars(content string) map[string]string {
	vars := map[string]string{}
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		eq := strings.Index(ln, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(ln[:eq], ":"), "+"))
		if name == "" || strings.ContainsAny(name, " \t$") {
			continue
		}
		if _, dup := vars[name]; dup {
			continue
		}
		vars[name] = strings.TrimSpace(ln[eq+1:])
	}
	return vars
}

func expandMakefileVars(s string, vars map[string]string) string {
	for pass := 0; pass < 2; pass++ {
		changed := false
		for name, val := range vars {
			ref := "$(" + name + ")"
			if strings.Contains(s, ref) {
				s = strings.ReplaceAll(s, ref, val)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return s
}

func splitBuildTagList(v string) []string {
	tag := strings.Trim(v, "'\"")
	tags := strings.Split(tag, ",")
	var result []string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !strings.HasPrefix(t, "$") {
			result = append(result, t)
		}
	}
	return result
}

func buildImportGraph(workingDir string) (map[string][]string, string) {
	if workingDir == "" {
		return nil, ""
	}

	importGraphCache.Lock()
	defer importGraphCache.Unlock()

	if importGraphCache.dir == workingDir &&
		time.Since(importGraphCache.data.builtAt) < importGraphTTL &&
		importGraphCache.data.graph != nil {
		return importGraphCache.data.graph, importGraphCache.data.modPath
	}

	kickImportGraphRebuildLocked(workingDir)

	if importGraphCache.data.graph == nil || importGraphCache.dir != workingDir {
		return nil, ""
	}

	return importGraphCache.data.graph, importGraphCache.data.modPath
}

func kickImportGraphRebuildLocked(workingDir string) {
	if importGraphCache.building {
		return
	}
	importGraphCache.building = true
	go func() {

		defer func() {
			importGraphCache.Lock()
			importGraphCache.building = false
			importGraphCache.Unlock()
		}()
		defer safego.Recover("agent.testImpact.build")
		graph, modPath := runGoList(workingDir)
		importGraphCache.Lock()
		if graph != nil {

			importGraphCache.data = importGraphEntry{
				graph:   graph,
				modPath: modPath,
				builtAt: time.Now(),
			}
			importGraphCache.dir = workingDir
			debug.Log("test-impact", "import graph built in background: %d packages in %s", len(graph), workingDir)
		}
		importGraphCache.Unlock()
	}()
}

func runGoList(workingDir string) (map[string][]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := []string{"list", "-f", "{{.ImportPath}}\x01{{join .Imports \",\"}}\x02{{join .TestImports \",\"}}\x03{{join .XTestImports \",\"}}"}
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
		var imports []string
		if len(parts) == 2 {

			fields := strings.Split(parts[1], "\x02")
			imports = appendNonEmpty(imports, strings.Split(fields[0], ","))
			if len(fields) > 1 {
				sub := strings.SplitN(fields[1], "\x03", 2)
				imports = appendNonEmpty(imports, strings.Split(sub[0], ","))
				if len(sub) > 1 {
					imports = appendNonEmpty(imports, strings.Split(sub[1], ","))
				}
			}
		}
		graph[importPath] = imports
	}
	return graph, modPath
}

func appendNonEmpty(dst []string, src []string) []string {
	for _, s := range src {
		s = strings.TrimSpace(s)
		if s != "" {
			dst = append(dst, s)
		}
	}
	return dst
}

func transitiveImporters(workingDir string, changedDirs []string) ([]string, bool) {
	graph, modPath := buildImportGraph(workingDir)
	if graph == nil || len(changedDirs) == 0 {
		return nil, graph != nil
	}

	targets := make(map[string]bool, len(changedDirs))
	for _, d := range changedDirs {
		targetPath := modPath + "/" + filepath.ToSlash(d)
		targets[targetPath] = true
	}
	debug.Log("test-impact", "transitiveImporters: initial targets=%v", targets)

	seen := make(map[string]bool)
	iteration := 0
	for len(targets) > 0 {
		iteration++
		debug.Log("test-impact", "transitiveImporters: iteration %d, targets=%d", iteration, len(targets))

		newTargets := make(map[string]bool)
		for pkgPath, imports := range graph {

			if seen[pkgPath] {
				continue
			}

			if targets[pkgPath] {
				continue
			}

			for _, imp := range imports {
				if targets[imp] {
					debug.Log("test-impact", "transitiveImporters: found importer %s imports %s", pkgPath, imp)
					newTargets[pkgPath] = true

					seen[pkgPath] = true
					debug.Log("test-impact", "transitiveImporters: marked seen %s", pkgPath)
					break
				}
			}
		}
		debug.Log("test-impact", "transitiveImporters: iteration %d found %d new importers", iteration, len(newTargets))

		for pkgPath := range newTargets {
			targets[pkgPath] = true
		}

		if len(newTargets) == 0 {
			break
		}
	}
	debug.Log("test-impact", "transitiveImporters: BFS complete, seen=%d packages", len(seen))

	if len(seen) == 0 {
		return nil, true
	}

	importers := make([]string, 0, len(seen))
	for pkgPath := range seen {
		relDir := strings.TrimPrefix(pkgPath, modPath+"/")

		if relDir != pkgPath && relDir != "" {
			importers = append(importers, filepath.ToSlash(relDir))
		} else {
			debug.Log("test-impact", "transitiveImporters: skipping %s (relDir=%s)", pkgPath, relDir)
		}
	}
	sort.Strings(importers)
	debug.Log("test-impact", "transitiveImporters: result=%v", importers)
	return importers, true
}

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

	importerDirs, graphOK := transitiveImporters(workingDir, changedDirs)

	allDirs := make(map[string]bool, len(changedDirs)+len(importerDirs))
	for _, d := range changedDirs {
		allDirs[d] = true
	}
	for _, d := range importerDirs {
		allDirs[d] = true
	}

	if len(allDirs) == 0 {
		return ""
	}

	changedSet := make(map[string]bool, len(changedDirs))
	for _, d := range changedDirs {
		changedSet[d] = true
	}

	dirList := make([]string, 0, len(allDirs))
	for d := range allDirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)

	var changedInList, importersInList []string
	for _, d := range dirList {
		if changedSet[d] {
			changedInList = append(changedInList, d)
		} else {
			importersInList = append(importersInList, d)
		}
	}

	const pkgCap = 20
	finalList := make([]string, 0, pkgCap)
	var omitted int
	for _, d := range changedInList {
		if len(finalList) >= pkgCap {
			omitted++
			continue
		}
		finalList = append(finalList, d)
	}
	for _, d := range importersInList {
		if len(finalList) >= pkgCap {
			omitted++
			continue
		}
		finalList = append(finalList, d)
	}

	parts := make([]string, len(finalList))
	for i, d := range finalList {
		parts[i] = goPkgArg(d)
	}

	cmd := "go test"
	if tags := detectGoBuildTags(workingDir); len(tags) > 0 {
		cmd += " -tags " + strings.Join(tags, ",")
	}
	cmd += " " + strings.Join(parts, " ")
	if omitted > 0 {
		cmd += fmt.Sprintf(" # +%d packages omitted", omitted)
	}
	if !graphOK {
		cmd += " # import graph unavailable, importers not included"
	}

	if len(importerDirs) > 0 {
		debug.Log("test-impact", "impact command with deps: %d changed + %d importers = %d total packages",
			len(changedDirs), len(importerDirs), len(allDirs))
	}
	return cmd
}
