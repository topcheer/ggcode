package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func changedGoFilesFromGit(workingDir string) []string {
	if workingDir == "" {
		return nil
	}
	cmd := exec.Command("git", "status", "--short", "--untracked-files=all")
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		debug.Log("test-impact", "git status failed in %s: %v", workingDir, err)
		return nil
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 4 {
			continue
		}

		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, filepath.ToSlash(path))
		}
	}
	return files
}

func changedGoPackageDirs(workingDir string) []string {
	files := changedGoFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var dirs []string
	for _, f := range files {
		dir := filepath.ToSlash(filepath.Dir(f))
		if dir == "" {
			continue
		}

		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func hasGoTestFile(workingDir, goFile string) bool {
	abs := goFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, goFile)
	}
	absDir := filepath.Dir(abs)

	matches, _ := filepath.Glob(filepath.Join(absDir, "*_test.go"))
	return len(matches) > 0
}

func untestedChangedFiles(workingDir string) []string {
	files := changedGoFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}
	var untested []string
	for _, f := range files {
		if !hasGoTestFile(workingDir, f) {
			untested = append(untested, f)
		}
	}
	return untested
}

func impactScopedTestCommand(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	dirs := changedGoPackageDirs(workingDir)
	if len(dirs) == 0 {
		return ""
	}

	if !fileExists(filepath.Join(workingDir, "go.mod")) {
		return ""
	}
	parts := make([]string, len(dirs))
	for i, d := range dirs {
		parts[i] = goPkgArg(d)
	}
	return "go test " + strings.Join(parts, " ")
}

func goPkgArg(d string) string {
	if d == "." {
		return "."
	}
	return "./" + d + "/"
}

func testCoverageNudge(workingDir string) string {
	untested := untestedChangedFiles(workingDir)
	if len(untested) == 0 {
		return ""
	}
	display := untested
	if len(display) > 5 {
		display = display[:5]
	}
	var b strings.Builder
	b.WriteString("[Test coverage gap: ")
	if len(untested) == 1 {
		b.WriteString("1 changed file has no test")
	} else {
		b.WriteString(fmt.Sprintf("%d changed files have no tests", len(untested)))
	}
	b.WriteString(": ")
	b.WriteString(strings.Join(display, ", "))
	if len(untested) > 5 {
		b.WriteString(fmt.Sprintf(", … (+%d more)", len(untested)-5))
	}
	b.WriteString(". Consider writing tests for these files.]")
	return b.String()
}
