package agent

// test_impact.go implements Test Impact Analysis (TIA) — a lightweight,
// zero-dependency approach to identifying which tests are affected by code
// changes and which changed code lacks test coverage.
//
// Competitor mapping:
//   - Cursor: suggests running affected tests after edits, based on AST analysis
//   - GitHub Copilot: "Generate Tests" based on function signatures
//   - Cline/OpenHands: checks test coverage in the agent loop
//   - JetBrains AI Assistant: identifies affected tests from code changes
//
// Our approach is purely computational (no LLM calls, no external deps):
//   1. Changed-file detection via `git status --short` (works in any repo state)
//   2. Package-level grouping to scope `go test` to affected packages
//   3. Test-file presence checks to surface untested code (test generation nudge)
//
// The functions here are intentionally Go-focused because Go has a predictable
// package/test-file convention (_test.go suffix). The pattern can be extended
// to other languages, but Go TIA is the highest-value target for this codebase.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// changedGoFilesFromGit returns Go source files (.go, excluding _test.go) that
// are modified, staged, or newly added relative to HEAD. It uses
// `git status --short --untracked-files=all` which works in any repo state
// (including new repos with no commits). Returns nil in non-git directories
// or when no Go files are changed.
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
		// git status --short format: "XY path" where XY is a 2-char status code.
		// For renames: "R  old -> new" — take the new path after " -> ".
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

// changedGoPackageDirs returns the unique set of directory paths (relative to
// workingDir, slash-separated) containing changed Go source files. This is the
// core of test impact analysis: by knowing which packages changed, we can
// scope `go test` to only those packages instead of running the full suite.
func changedGoPackageDirs(workingDir string) []string {
	files := changedGoFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var dirs []string
	for _, f := range files {
		dir := filepath.ToSlash(filepath.Dir(f))
		if dir == "." || dir == "" {
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

// hasGoTestFile checks whether a Go source file has a corresponding _test.go
// file in the same directory. For "foo.go", it checks for "foo_test.go".
// This identifies whether the changed code has test coverage — a prerequisite
// for test generation suggestions.
func hasGoTestFile(workingDir, goFile string) bool {
	abs := goFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, goFile)
	}
	absDir := filepath.Dir(abs)
	base := filepath.Base(goFile)
	name := strings.TrimSuffix(base, ".go")
	testFile := name + "_test.go"
	_, err := os.Stat(filepath.Join(absDir, testFile))
	return err == nil
}

// untestedChangedFiles returns changed Go source files that lack a
// corresponding _test.go file. These are candidates for test generation.
// The nudge to write tests for these files is the "automated test generation
// suggestion" — it proactively surfaces coverage gaps without requiring the
// user to explicitly ask.
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

// impactScopedTestCommand builds a `go test` command covering all changed Go
// packages. For example, if files in internal/agent/ and internal/util/ were
// both changed, it returns "go test ./internal/agent/ ./internal/util/".
// Returns "" if no changed packages are found, workingDir is empty, or the
// directory is not a Go module.
//
// This replaces the old approach of always suggesting `go build ./...` (full
// suite) with a targeted, impact-aware command that runs only the tests for
// packages that actually changed — dramatically faster for large monorepos.
func impactScopedTestCommand(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	dirs := changedGoPackageDirs(workingDir)
	if len(dirs) == 0 {
		return ""
	}
	// Only applicable for Go modules.
	if !fileExists(filepath.Join(workingDir, "go.mod")) {
		return ""
	}
	parts := make([]string, len(dirs))
	for i, d := range dirs {
		parts[i] = "./" + d + "/"
	}
	return "go test " + strings.Join(parts, " ")
}

// testCoverageNudge generates a hint string about changed Go files that lack
// test coverage. This is the "automated test generation" component: instead of
// waiting for the user to ask for tests, the agent proactively learns which
// files have no tests and can suggest generating them.
//
// Returns "" when there are no untested files or the hint would be too noisy
// (capped at 5 files to avoid bloating the verification hint).
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
