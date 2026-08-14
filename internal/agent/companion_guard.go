package agent

// Change Set Companion File Guard — Paired File Test Coverage Awareness
//
// Research: Claude Code, Cursor, Aider, and Devin all face the same failure
// mode: the agent edits source files but forgets to update the corresponding
// test files. This produces incomplete changes that pass the build but miss
// test coverage for the new/modified code.
//
//   - Claude Code: system prompt says "run tests" but no automated companion
//     file check. The agent decides on its own whether to update tests.
//   - Cursor: no companion file awareness in Composer mode.
//   - Aider: user must explicitly add test files to the chat session.
//   - Devin: uses expensive LLM-based test generation suggestions.
//   - OpenHands: has a separate "Test Writer" agent (LLM-based, costs tokens).
//
// ggcode already has test_impact.go which identifies which tests to RUN after
// edits. But NOTHING checks whether the agent UPDATED the test files. This
// guard fills that gap:
//
//   1. TRACK: collect all files edited during the run (from runStats.FilesEdited).
//   2. DETECT: for each edited non-test source file, check if a companion test
//      file exists on disk (e.g. foo.go → foo_test.go).
//   3. WARN: if the companion test file exists but was NOT edited in this run,
//      inject a reminder before the agent returns.
//
// The check fires AT MOST ONCE per run, at the pre-completion exit point
// (alongside the fulfillment gate and complexity gate). Zero LLM cost —
// deterministic file pattern matching only.
//
// Supported language patterns:
//   - Go:         foo.go → foo_test.go
//   - Python:     foo.py → test_foo.py, foo_test.py
//   - JavaScript: foo.js → foo.test.js, foo.spec.js
//   - TypeScript: foo.ts → foo.test.ts, foo.spec.ts
//   - Java:       Foo.java → FooTest.java, FooTests.java
//   - Rust:       foo.rs → foo.rs (in tests/ dir or #[test] inline — skipped)
//   - C/C++:      foo.c → test_foo.c, foo_test.c

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxCompanionGuardWarnings = 1

// companionGuardState tracks whether the guard has already fired this run.
type companionGuardState struct {
	fired bool
}

func newCompanionGuardState() *companionGuardState {
	return &companionGuardState{}
}

func (c *companionGuardState) reset() {
	c.fired = false
}

// isTestFilePath returns true if the path looks like a test file for any supported
// language. Used to avoid warning about test files that have no companion test.
func isTestFilePath(path string) bool {
	base := filepath.Base(path)
	for _, suffix := range []string{
		"_test.go",         // Go
		"_test.",           // Python (foo_test.py)
		".test.", ".spec.", // JS/TS
		"Test.java",  // Java
		"Tests.java", // Java
		"IT.java",    // Java integration tests
	} {
		if strings.Contains(base, suffix) {
			return true
		}
	}
	// Python test_ prefix: use HasPrefix to avoid matching files like
	// latest_news.py, protest_banner.py, contest_rules.py (issue #23).
	if strings.HasPrefix(base, "test_") {
		return true
	}
	return false
}

// companionTestPaths returns the candidate test file paths for a given source
// file path. The source file must NOT already be a test file.
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
		// Go: foo.go → foo_test.go
		candidates = append(candidates, filepath.Join(dir, name+"_test.go"))

	case ".py":
		// Python: foo.py → test_foo.py, foo_test.py
		candidates = append(candidates,
			filepath.Join(dir, "test_"+name+".py"),
			filepath.Join(dir, name+"_test.py"),
		)
		// Also check tests/ subdirectory (pytest convention)
		parentDir := filepath.Dir(dir)
		candidates = append(candidates,
			filepath.Join(parentDir, "tests", "test_"+name+".py"),
			filepath.Join(parentDir, "test", "test_"+name+".py"),
		)

	case ".js", ".jsx", ".mjs", ".cjs":
		// JavaScript: foo.js → foo.test.js, foo.spec.js
		candidates = append(candidates,
			filepath.Join(dir, name+".test"+ext),
			filepath.Join(dir, name+".spec"+ext),
		)

	case ".ts", ".tsx":
		// TypeScript: foo.ts → foo.test.ts, foo.spec.ts
		candidates = append(candidates,
			filepath.Join(dir, name+".test"+ext),
			filepath.Join(dir, name+".spec"+ext),
		)

	case ".java":
		// Java: Foo.java → FooTest.java, FooTests.java
		candidates = append(candidates,
			filepath.Join(dir, name+"Test.java"),
			filepath.Join(dir, name+"Tests.java"),
		)

	case ".c", ".cpp", ".cc", ".h", ".hpp":
		// C/C++: foo.c → test_foo.c, foo_test.c
		candidates = append(candidates,
			filepath.Join(dir, "test_"+name+ext),
			filepath.Join(dir, name+"_test"+ext),
		)

	case ".rb":
		// Ruby: foo.rb → foo_spec.rb, foo_test.rb
		candidates = append(candidates,
			filepath.Join(dir, name+"_spec.rb"),
			filepath.Join(dir, name+"_test.rb"),
		)
	}

	return candidates
}

// shouldSkipCompanionCheck returns true for files that should not trigger
// companion file warnings (generated files, vendored code, configs, etc.).
func shouldSkipCompanionCheck(path string) bool {
	// Normalize for matching — lowercase the full path.
	lower := strings.ToLower(filepath.ToSlash(path))

	// Vendor and third-party directories — check both /dir/ and dir/ (at start).
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

	// Generated files
	base := filepath.Base(lower)
	if strings.HasSuffix(base, ".gen.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		strings.HasSuffix(base, ".generated.go") ||
		strings.Contains(base, ".pb.") || // protobuf generated
		strings.Contains(base, ".gen.") ||
		strings.HasPrefix(base, "zz_generated") {
		return true
	}

	// Config and non-code files
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

	// Files in testdata directories
	if strings.Contains(lower, "/testdata/") {
		return true
	}

	return false
}

// resolvePath resolves a file path relative to the working directory if it's
// not already absolute. Returns the resolved path and true if the file exists.
func companionFileExists(workDir, relPath string) bool {
	fullPath := relPath
	if !filepath.IsAbs(fullPath) && workDir != "" {
		fullPath = filepath.Join(workDir, relPath)
	}
	_, err := os.Stat(fullPath)
	return err == nil
}

// checkCompanionFiles analyzes the set of edited files and returns a non-empty
// warning message if companion test files exist but were not edited.
//
// Parameters:
//   - runStats: accumulated stats from the run (FilesEdited is the key field)
//
// Returns "" when:
//   - the guard already fired this run
//   - no source files were edited
//   - all companion test files were also edited
//   - no companion test files exist on disk
func (a *companionGuardState) checkCompanionFiles(runStats *RunStats, workDir string) string {
	if a.fired {
		return ""
	}
	a.fired = true

	if len(runStats.FilesEdited) == 0 {
		return ""
	}

	// Build a set of edited files for O(1) lookup.
	edited := make(map[string]bool, len(runStats.FilesEdited))
	for _, f := range runStats.FilesEdited {
		// Normalize for comparison: use the raw path as given by the tool.
		edited[normalizeCompanionPath(workDir, f)] = true
	}

	// Find source files with existing but unedited companion test files.
	type missing struct {
		src  string
		test string
	}
	var missingCompanions []missing

	for _, f := range runStats.FilesEdited {
		// Skip test files themselves
		if isTestFilePath(f) {
			continue
		}
		// Skip non-code files
		if shouldSkipCompanionCheck(f) {
			continue
		}

		candidates := companionTestPaths(f)
		for _, candidate := range candidates {
			if !companionFileExists(workDir, candidate) {
				continue
			}
			// Test file exists on disk. Was it edited in this run?
			if edited[normalizeCompanionPath(workDir, candidate)] {
				continue // Already updated — good.
			}
			missingCompanions = append(missingCompanions, missing{
				src:  filepath.Base(f),
				test: candidate,
			})
			break // One warning per source file is enough.
		}
	}

	if len(missingCompanions) == 0 {
		return ""
	}

	// Build the warning message. Cap at 5 files to avoid context bloat.
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

// normalizeCompanionPath converts a file path to a canonical absolute form for
// comparison. Relative paths are resolved against workDir so that a source file
// edited via a relative path still matches its test companion edited via an
// absolute path (and vice versa). Issue #310: the old implementation only ran
// filepath.Clean, so mixed relative/absolute forms caused false "test files
// were not updated" warnings.
func normalizeCompanionPath(workDir, p string) string {
	if !filepath.IsAbs(p) && workDir != "" {
		p = filepath.Join(workDir, p)
	}
	// Clean the path to resolve ./ and ../ components.
	return filepath.Clean(p)
}
