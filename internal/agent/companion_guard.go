package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type companionGuardState struct {
	fired bool

	// selfAuthored tracks test files that this flow itself wrote or changed
	// relative to the worktree base commit, refreshed from git provenance so
	// tests authored in an earlier run of a worktree flow (already committed
	// on the worktree branch) are not reported as "not updated" (#4790).
	selfAuthored map[string]bool

	base string
}

func newCompanionGuardState() *companionGuardState {
	return &companionGuardState{
		selfAuthored: make(map[string]bool),
	}
}

func (c *companionGuardState) reset() {
	c.fired = false
	c.selfAuthored = make(map[string]bool)
	c.base = ""
}

// refreshProvenance recomputes the set of files changed relative to the
// worktree base commit. It is invoked when the agent enters a worktree (a
// tool result suggesting a new working dir) and before the companion
// advisory is emitted, so the guard judges companions against what THIS
// flow authored instead of stale tracking state (#4790).
func (c *companionGuardState) refreshProvenance(workDir string) {
	base := worktreeBaseCommit(workDir)
	if base == "" {
		return
	}
	if c.base == base {
		return
	}
	c.base = base
	c.selfAuthored = make(map[string]bool)
	for _, f := range gitChangedSince(workDir, base) {
		c.selfAuthored[normalizeCompanionPath(workDir, f)] = true
	}
	if len(c.selfAuthored) > 0 {
		debug.Log("agent", "companion file guard: %d file(s) self-authored relative to base %s", len(c.selfAuthored), base)
	}
}

func isTestFilePath(path string) bool {
	base := filepath.Base(path)
	for _, suffix := range []string{
		"_test.go",
		"_test.",
		".test.", ".spec.",
		"Test.java",
		"Tests.java",
		"IT.java",
	} {
		if strings.Contains(base, suffix) {
			return true
		}
	}

	if strings.HasPrefix(base, "test_") {
		return true
	}
	return false
}

func companionTestPaths(srcPath string) []string {
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" || ext == "" {
		return nil
	}

	var candidates []string

	switch ext {
	case ".go":

		candidates = append(candidates, filepath.Join(dir, name+"_test.go"))

	case ".py":

		candidates = append(candidates,
			filepath.Join(dir, "test_"+name+".py"),
			filepath.Join(dir, name+"_test.py"),
		)

		parentDir := filepath.Dir(dir)
		candidates = append(candidates,
			filepath.Join(parentDir, "tests", "test_"+name+".py"),
			filepath.Join(parentDir, "test", "test_"+name+".py"),
		)

	case ".js", ".jsx", ".mjs", ".cjs":

		candidates = append(candidates,
			filepath.Join(dir, name+".test"+ext),
			filepath.Join(dir, name+".spec"+ext),
		)

	case ".ts", ".tsx":

		candidates = append(candidates,
			filepath.Join(dir, name+".test"+ext),
			filepath.Join(dir, name+".spec"+ext),
		)

	case ".java":

		candidates = append(candidates,
			filepath.Join(dir, name+"Test.java"),
			filepath.Join(dir, name+"Tests.java"),
		)

	case ".c", ".cpp", ".cc", ".h", ".hpp":

		candidates = append(candidates,
			filepath.Join(dir, "test_"+name+ext),
			filepath.Join(dir, name+"_test"+ext),
		)

	case ".rb":

		candidates = append(candidates,
			filepath.Join(dir, name+"_spec.rb"),
			filepath.Join(dir, name+"_test.rb"),
		)
	}

	return candidates
}

func shouldSkipCompanionCheck(path string) bool {

	lower := strings.ToLower(filepath.ToSlash(path))

	for _, skip := range []string{
		"vendor/",
		"node_modules/",
		"third_party/",
		".git/",
		"dist/",
		"build/",
		"target/",
	} {
		if strings.Contains(lower, "/"+skip) || strings.HasPrefix(lower, skip) {
			return true
		}
	}

	base := filepath.Base(lower)
	if strings.HasSuffix(base, ".gen.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		strings.HasSuffix(base, ".generated.go") ||
		strings.Contains(base, ".pb.") ||
		strings.Contains(base, ".gen.") ||
		strings.HasPrefix(base, "zz_generated") {
		return true
	}

	for _, ext := range []string{
		".json", ".yaml", ".yml", ".toml", ".ini", ".cfg",
		".md", ".txt", ".rst",
		".sql", ".proto", ".thrift",
		".html", ".css", ".scss", ".less",
		".svg", ".png", ".jpg", ".jpeg", ".gif", ".ico",
		".lock", ".sum",
	} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	if strings.Contains(lower, "/testdata/") {
		return true
	}

	return false
}

func companionFileExists(workDir, relPath string) bool {
	fullPath := relPath
	if !filepath.IsAbs(fullPath) && workDir != "" {
		fullPath = filepath.Join(workDir, relPath)
	}
	_, err := os.Stat(fullPath)
	return err == nil
}

func (a *companionGuardState) checkCompanionFiles(runStats *RunStats, workDir string) string {
	if a.fired {
		return ""
	}
	a.fired = true

	if len(runStats.FilesEdited) == 0 {
		return ""
	}

	a.refreshProvenance(workDir)

	edited := make(map[string]bool, len(runStats.FilesEdited))
	for _, f := range runStats.FilesEdited {

		edited[normalizeCompanionPath(workDir, f)] = true
	}

	type missing struct {
		src  string
		test string
	}
	var missingCompanions []missing

	for _, f := range runStats.FilesEdited {

		if isTestFilePath(f) {
			continue
		}

		if shouldSkipCompanionCheck(f) {
			continue
		}

		candidates := companionTestPaths(f)
		for _, candidate := range candidates {
			if !companionFileExists(workDir, candidate) {
				continue
			}

			if edited[normalizeCompanionPath(workDir, candidate)] {
				continue
			}
			if a.selfAuthored[normalizeCompanionPath(workDir, candidate)] {
				debug.Log("agent", "companion file guard: skipping %s (self-authored earlier in this worktree flow)", candidate)
				continue
			}
			missingCompanions = append(missingCompanions, missing{
				src:  filepath.Base(f),
				test: candidate,
			})
			break
		}
	}

	if len(missingCompanions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Companion File Check] You edited source files that have ")
	sb.WriteString("existing test files, but those test files were not updated in this run.\n")
	sb.WriteString("Review whether the tests need updating for your changes:\n")

	limit := len(missingCompanions)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		m := missingCompanions[i]
		sb.WriteString(fmt.Sprintf("  - %s → %s\n", m.src, m.test))
	}
	if len(missingCompanions) > 5 {
		sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(missingCompanions)-5))
	}
	sb.WriteString("\nIf your changes don't affect test behavior, you can safely ignore this. ")
	sb.WriteString("Otherwise, update the tests to cover your changes before finishing.")

	debug.Log("agent", "companion file guard: %d source files with unedited test companions", len(missingCompanions))
	return sb.String()
}

func normalizeCompanionPath(workDir, p string) string {
	if !filepath.IsAbs(p) && workDir != "" {
		p = filepath.Join(workDir, p)
	}

	return filepath.Clean(p)
}
