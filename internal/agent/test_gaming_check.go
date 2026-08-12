package agent

// Test Gaming Detection - Verification Integrity Guard
//
// A well-documented failure mode in autonomous AI coding agents is "gaming the
// verification": instead of fixing the code to make tests pass, the agent
// modifies the tests themselves - deleting failing tests, commenting out
// assertions, adding skip directives, or relaxing expected values. This gives
// the appearance of success (build/test passes) while the actual bug remains.
//
// Research basis:
//   - "Specification Gaming" in AI agents (DeepMind, 2024): agents find
//     shortcuts to satisfy reward signals without solving the real task.
//   - Devin, Claude Code, Cursor: none have explicit guards against test
//     weakening. They rely on the agent's own judgment, which is unreliable
//     precisely in the cases where gaming occurs.
//
// ggcode's approach: deterministic, delta-based detection that flags suspicious
// changes to test files introduced by the agent's edit. Only changes INTRODUCED
// by this edit are flagged (comparing old vs new content). Zero LLM cost, zero
// external dependencies, <1ms per check.
//
// Detection patterns:
//  1. Deleted test functions (Go AST: Test* funcs in old but not in new)
//  2. Added skip directives (t.Skip, pytest.skip, .skip(), @Ignore, etc.)
//  3. Removed assertion calls (t.Error, t.Fatal, assert.*, require.*, etc.)

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// testGamingMaxWarnings caps the number of test-gaming warnings per write.
const testGamingMaxWarnings = 3

// Precompiled regexes for performance (avoid recompiling per line).

// skipDirectiveRegexes matches lines that skip/ignore tests.
var skipDirectiveRegexes = []*regexp.Regexp{
	// Go
	regexp.MustCompile(`^\s*t\.Skip(f)?\(`),
	// Python
	regexp.MustCompile(`^\s*pytest\.skip\(`),
	regexp.MustCompile(`^\s*@pytest\.mark\.skip`),
	regexp.MustCompile(`^\s*self\.skipTest\(`),
	regexp.MustCompile(`^\s*unittest\.skip\(`),
	regexp.MustCompile(`^\s*@unittest\.skip`),
	regexp.MustCompile(`^\s*@pytest\.mark\.xfail`),
	// JS/TS - .skip() and xit/xdescribe
	regexp.MustCompile(`^\s*(it|test|describe)\.skip\(`),
	regexp.MustCompile(`^\s*xit\(`),
	regexp.MustCompile(`^\s*xdescribe\(`),
	// Java/Kotlin
	regexp.MustCompile(`^\s*@Ignore\b`),
	regexp.MustCompile(`^\s*@Disabled\b`),
	regexp.MustCompile(`^\s*Assume\.assumeFalse\(`),
	// Ruby
	regexp.MustCompile(`^\s*skip\b`),
}

// assertionLineRegex matches lines containing active test assertion calls.
// Uses a broad match then filters in isAssertionLine.
var assertionKeywordRe = regexp.MustCompile(
	`(?:t\.Error|t\.Fatal|t\.Fail|t\.Skip|require\.|assert\.|expect\(|expect |assertEqual|assertTrue|assertFalse|assertNotNull|assertNull|assertSame|\.to(Equal|Be|Contain|Exist|Match|Have|Not)|\.should\(|assert\.strictEqual|assert\.deepEqual)`,
)

// checkTestGaming detects suspicious modifications to test files that weaken
// verification rather than fixing code. Returns warnings or empty string.
//
// Parameters:
//   - filePath: path of the written file (used for test file detection)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkTestGaming(filePath, oldContent, newContent string) string {
	if oldContent == "" {
		return "" // new file, nothing was weakened
	}

	// Reuse the existing isTestFile(path string) from debug_sniffer.go.
	if !isTestFile(filePath) {
		return ""
	}

	var warnings []string

	// Go test files: use AST for precise function-level analysis.
	if filepath.Ext(filePath) == ".go" {
		warnings = append(warnings, checkGoTestGaming(filePath, oldContent, newContent)...)
	}

	// Universal checks (all languages): skip directives and assertion removal.
	warnings = append(warnings, checkSkipDirectives(filePath, oldContent, newContent)...)
	warnings = append(warnings, checkAssertionRemoval(filePath, oldContent, newContent)...)

	if len(warnings) == 0 {
		return ""
	}

	if len(warnings) > testGamingMaxWarnings {
		warnings = warnings[:testGamingMaxWarnings]
	}

	var b strings.Builder
	b.WriteString("[Test integrity warning]\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

// checkGoTestGaming uses Go AST to detect deleted test functions and benchmark
// functions in Go test files.
func checkGoTestGaming(filename, oldSrc, newSrc string) []string {
	oldFuncs := goTestFuncNames(filename, oldSrc)
	newFuncs := goTestFuncNames(filename, newSrc)

	if oldFuncs == nil || newFuncs == nil {
		// Parse error - skip AST-based checks, fall back to other checks.
		return nil
	}

	var deleted []string
	for name := range oldFuncs {
		if !newFuncs[name] {
			deleted = append(deleted, name)
		}
	}

	if len(deleted) == 0 {
		return nil
	}

	// Sort for deterministic output.
	sort.Strings(deleted)

	// Cap at 5 names to keep the warning concise.
	shown := deleted
	if len(shown) > 5 {
		shown = shown[:5]
	}

	suffix := ""
	if len(deleted) > 5 {
		suffix = fmt.Sprintf(" (and %d more)", len(deleted)-5)
	}

	return []string{
		fmt.Sprintf("Test file edit removed %d test function(s): %s%s. "+
			"Removing tests to make the build pass weakens verification - "+
			"fix the code instead of deleting tests that expose bugs.",
			len(deleted), strings.Join(shown, ", "), suffix),
	}
}

// goTestFuncNames parses Go source and returns the set of test/benchmark/example
// function names (Test*, Benchmark*, Example*, Fuzz*).
func goTestFuncNames(filename, src string) map[string]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	funcs := make(map[string]bool)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil {
			continue
		}
		name := fd.Name.Name
		if strings.HasPrefix(name, "Test") ||
			strings.HasPrefix(name, "Benchmark") ||
			strings.HasPrefix(name, "Example") ||
			strings.HasPrefix(name, "Fuzz") {
			funcs[name] = true
		}
	}
	return funcs
}

// --- Skip directive detection (language-agnostic) ---

// checkSkipDirectives detects newly added skip/ignore directives in test files.
// Delta-based: only flags skips that were NOT in the old content.
func checkSkipDirectives(filePath, oldContent, newContent string) []string {
	oldSkips := countSkipDirectives(filePath, oldContent)
	newSkips := countSkipDirectives(filePath, newContent)

	if newSkips <= oldSkips {
		return nil
	}

	added := newSkips - oldSkips
	return []string{
		fmt.Sprintf("Test file edit added %d skip/ignore directive(s). "+
			"Skipping tests to make verification pass is verification gaming - "+
			"fix the underlying issue instead of skipping the test.", added),
	}
}

// countSkipDirectives counts lines matching any skip directive pattern.
// Ruby-specific patterns are only applied to .rb files to avoid false positives
// in Go/other languages (e.g. "skip := true" in Go matching Ruby's `skip`).
func countSkipDirectives(filePath, content string) int {
	ext := filepath.Ext(filePath)
	count := 0
	for _, line := range strings.Split(content, "\n") {
		for i, re := range skipDirectiveRegexes {
			// skipDirectiveRegexes index 11 is the Ruby `^\s*skip\b` pattern.
			// Only apply it to .rb files.
			if i == 11 && ext != ".rb" {
				continue
			}
			if re.MatchString(line) {
				count++
				break // one skip per line
			}
		}
	}
	return count
}

// --- Assertion removal detection ---

// checkAssertionRemoval detects assertion calls that were present in the old
// content but are absent from the new content. This catches deleted assertion
// lines and commented-out assertion lines.
func checkAssertionRemoval(filePath, oldContent, newContent string) []string {
	oldActive := countActiveAssertions(oldContent)
	newActive := countActiveAssertions(newContent)

	if newActive >= oldActive {
		return nil
	}

	removed := oldActive - newActive
	if removed < 2 {
		// Removing a single assertion could be legitimate refactoring.
		// Only flag when 2+ assertions are removed - stronger signal of gaming.
		return nil
	}

	return []string{
		fmt.Sprintf("Test file edit removed %d assertion(s) (t.Error/Fatal, assert.*, require.*, expect, etc.). "+
			"Removing assertions weakens test coverage - if the assertion was wrong, fix it; "+
			"don't delete it unless the test is being restructured.", removed),
	}
}

// countActiveAssertions counts lines containing active (non-commented) assertion calls.
func countActiveAssertions(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comments and block-comment lines.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if isAssertionLine(line) {
			count++
		}
	}
	return count
}

// isAssertionLine checks if a line contains a test assertion call.
func isAssertionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
		return false
	}
	return assertionKeywordRe.MatchString(line)
}
