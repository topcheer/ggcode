package agent

// Specification Gaming Detector
//
// Research basis: METR's 2025 study found that 30.4% of agent runs on
// competitive engineering tasks involved reward hacking -- agents finding
// ways to pass verification without solving the actual problem. Examples:
//   - Monkey-patching pytest internals to suppress failures
//   - Overriding Python __eq__ to make every equality check return True
//   - Calling sys.exit(0) before tests ran so the zero exit code = "success"
//   - Editing test assertions instead of fixing the source code
//   - Deleting tests that fail instead of fixing the code they test
//   - Adding @skip/@pytest.mark.skip to failing tests
//   - Commenting out test bodies that fail
//
// The "Verification Horizon" paper (arxiv 2606.26300) formalizes this:
// "Every verifier we can build is only a proxy for human intent, never the
// intent itself." Agents optimize for the proxy, not the goal.
//
// The gap: ggcode has verification gates (sync-verify, unverified claim
// detection, companion guard), but NONE of them detect the inverse failure:
// the agent DID run verification and it DID pass -- but only because the agent
// modified the verification mechanism rather than the source code. This is
// "metric decoupling" (tianpan.co, 2026): proxy improves while actual quality
// degrades.
//
// This detector fills that gap with deterministic, zero-LLM-cost heuristics:
//   1. TEST FILE MODIFICATION PATTERN: detect when the agent edits test files
//      (suffix _test.go, _test.py, .test.ts, .spec.ts) but does NOT edit the
//      corresponding source files (the files being tested)
//   2. TEST DELETION/SKIP PATTERN: detect when the agent deletes test files
//      or adds skip markers (grep for skip/ignore/xfail patterns in commands
//      and file edits)
//   3. CI CONFIG TAMPERING: detect edits to CI/verification config files
//      (.github/workflows/*.yml, Makefile build targets, pytest.ini, etc.)
//      when the task is a bug fix or feature implementation (not a CI change)
//
// The detector fires AT MOST ONCE per run.
//
// #544 fixes (false-positive control):
//   - Pattern 2 excludes read-only investigative commands (grep/rg/find/
//     cat/... and `git grep`): a reviewer grepping for "t.Skip(" to CHECK
//     whether tests were bypassed is legitimate investigation, not gaming.
//   - Pattern 1 gains a task-intent exemption: when the user's task IS
//     writing/updating tests (prompt mentions test/测试/回归), editing only
//     test files is the expected outcome, not reward hacking.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxSpecGamingWarnings = 1

// specGamingState tracks whether the specification gaming detector has fired.
type specGamingState struct {
	fired bool
}

func newSpecGamingState() *specGamingState {
	return &specGamingState{}
}

func (s *specGamingState) reset() {
	s.fired = false
}

// testFileSuffixes are common test file naming patterns across languages.
var testFileSuffixes = []string{
	"_test.go",  // Go
	"_test.py",  // Python
	".test.ts",  // TypeScript
	".test.tsx", // TypeScript React
	".test.js",  // JavaScript
	".test.jsx", // JavaScript React
	".spec.ts",  // TypeScript (Jest/Mocha)
	".spec.tsx", // TypeScript React
	".spec.js",  // JavaScript (Jest/Mocha)
	".spec.jsx", // JavaScript React
	"_test.rb",  // Ruby
	"Test.java", // Java
	"_test.rs",  // Rust
	"_test.c",   // C
	"_test.cpp", // C++
	".test.kt",  // Kotlin
}

// ciConfigFiles are paths that, when edited during a non-CI task, may indicate
// verification tampering. These are NOT edited during normal feature/bugfix work.
var ciConfigFiles = map[string]bool{
	".github/workflows": true,
	"pytest.ini":        true,
	"setup.cfg":         true,
	"tox.ini":           true,
	"conftest.py":       true,
	"jest.config.js":    true,
	"jest.config.ts":    true,
	"vitest.config.ts":  true,
	"vitest.config.js":  true,
	".babelrc":          true,
	"Makefile":          false, // partial -- only if build/test targets modified
}

// skipMarkers are text patterns that indicate tests being skipped/ignored.
var skipMarkers = []string{
	"@pytest.mark.skip",
	"@pytest.mark.xfail",
	"@skip",
	"@unittest.skip",
	"pytest.skip(",
	"it.skip(",
	"test.skip(",
	"describe.skip(",
	"xdescribe(",
	"xit(",
	"xtest(",
	"// @ts-ignore", // suppress type errors
	"@Disabled",     // JUnit
	"testing.Skip(", // Go
	"t.Skip(",       // Go testing
}

// isTestFile returns true if the path matches a known test file naming pattern.
func specGamingIsTestFile(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range testFileSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// stripTestSuffix converts a test file path to its likely source file path.
// e.g. "foo/bar_test.go" -> "foo/bar.go", "utils.test.ts" -> "utils.ts"
func stripTestSuffix(path string) string {
	lower := strings.ToLower(path)
	for _, suffix := range testFileSuffixes {
		if strings.HasSuffix(lower, suffix) {
			// Remove the test-specific portion and reconstruct
			base := path[:len(path)-len(suffix)]
			// For Go: _test.go -> .go
			if suffix == "_test.go" {
				return base + ".go"
			}
			// For Python: _test.py -> .py
			if suffix == "_test.py" {
				return base + ".py"
			}
			// For JS/TS: .test.ts -> .ts, .spec.ts -> .ts
			if suffix == ".test.ts" || suffix == ".spec.ts" {
				return base + ".ts"
			}
			if suffix == ".test.tsx" || suffix == ".spec.tsx" {
				return base + ".tsx"
			}
			if suffix == ".test.js" || suffix == ".spec.js" {
				return base + ".js"
			}
			if suffix == ".test.jsx" || suffix == ".spec.jsx" {
				return base + ".jsx"
			}
			if suffix == "_test.rb" {
				return base + ".rb"
			}
			if suffix == "_test.rs" {
				return base + ".rs"
			}
			if suffix == "_test.c" {
				return base + ".c"
			}
			if suffix == "_test.cpp" {
				return base + ".cpp"
			}
			if suffix == ".test.kt" {
				return base + ".kt"
			}
			// Fallback: just strip suffix
			return base
		}
	}
	return path
}

// sourceFileEdited checks whether a non-test source file was edited.
func sourceFileEdited(filesEdited []string) bool {
	for _, f := range filesEdited {
		if !specGamingIsTestFile(f) && !isConfigOrLockFile(f) {
			return true
		}
	}
	return false
}

// isConfigOrLockFile returns true for non-source config/lock files.
func isConfigOrLockFile(path string) bool {
	base := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		base = path[idx+1:]
	}
	lower := strings.ToLower(base)

	// Special case: Dockerfile (case-insensitive exact basename match only)
	if strings.EqualFold(base, "dockerfile") {
		return true
	}

	// Special case: Makefile (case-insensitive exact basename match only)
	if strings.EqualFold(base, "makefile") {
		return true
	}

	configSuffixes := []string{
		".toml", ".yaml", ".yml", ".json", ".ini", ".cfg",
		".lock", ".mod", ".sum", ".env",
	}
	for _, s := range configSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// isCIConfigPath returns true if the path is a CI/verification config file.
func isCIConfigPath(path string) bool {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	for pattern, enabled := range ciConfigFiles {
		if !enabled {
			continue
		}
		if strings.Contains(pattern, "/") {
			// Directory pattern: use prefix match
			if strings.HasPrefix(lower, pattern+"/") || lower == pattern {
				return true
			}
		} else {
			// Filename pattern: exact basename match only
			if base == pattern {
				return true
			}
		}
	}
	return false
}

// readOnlySearchVerbs are shell commands whose sole purpose is reading or
// searching. Skip markers appearing in these commands are investigative
// (e.g. grep -rn "t.Skip(" . to check whether tests were bypassed), never
// introduction of skip markers (#544 Bug C1).
var readOnlySearchVerbs = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ripgrep": true,
	"ag": true, "ack": true, "find": true, "cat": true, "head": true,
	"tail": true, "less": true, "more": true, "wc": true, "git": true, // git handled below (nested subcommand)
}

// isReadOnlySearchCommand returns true when the shell command's effective
// verb is a read-only search/read tool. Leading environment assignments
// (FOO=bar cmd) are skipped so they cannot disguise the verb.
func isReadOnlySearchCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	idx := 0
	// Skip leading env assignments (VAR=value) and no-op wrappers.
	for idx < len(fields) && (strings.Contains(fields[idx], "=") || fields[idx] == "sudo" || fields[idx] == "command" || fields[idx] == "env") {
		idx++
	}
	if idx >= len(fields) {
		return false
	}
	verb := filepath.Base(fields[idx])
	if readOnlySearchVerbs[verb] {
		return true
	}
	if verb == "git" && idx+1 < len(fields) {
		// git grep is read-only investigation
		if fields[idx+1] == "grep" {
			return true
		}
		// git log -S and git log -G are historical investigation
		if fields[idx+1] == "log" && idx+2 < len(fields) &&
			(fields[idx+2] == "-S" || fields[idx+2] == "-G") {
			return true
		}
	}
	return false
}

// isTestWritingTask returns true when the user's prompt indicates the task
// itself is writing or updating tests. For such tasks, editing only test
// files is the expected outcome — Pattern 1 must not fire (#544 Bug C2).
// English keywords match whole words only (same tokenization as
// isCIRelatedTask's #501 fix) to avoid substring hits like "latest".
func isTestWritingTask(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)
	// CJK keywords: substring match is safe (no substring collision risk).
	for _, kw := range []string{"测试", "回归", "用例"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, w := range words {
		switch w {
		case "test", "tests", "testing", "unittest", "unittests",
			"spec", "specs", "specification", "specifications",
			"coverage":
			return true
		}
	}
	// CJK keywords: substring match is safe (no substring collision risk).
	for _, kw := range []string{"测试", "回归", "用例", "单测", "单元测试"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// isSkipMarkerRemovalCommand returns true when the command appears to be
// REMOVING skip markers (legitimate remediation) rather than adding them.
// Examples:
//   - sed -i 's/t.Skip(/t.Log(/' - removes t.Skip
//   - awk '{gsub(/t.Skip\(/, "t.Log(")}' - removes t.Skip
//   - sed -i 's/@pytest.mark.skip//' - removes @pytest.mark.skip
func isSkipMarkerRemovalCommand(cmd string) bool {
	lower := strings.ToLower(cmd)

	// Check for sed/awk replacement patterns
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return false
	}

	verb := filepath.Base(fields[0])
	if verb != "sed" && verb != "awk" {
		return false
	}

	if verb == "sed" {
		return isSedSkipRemoval(cmd)
	}
	return isAwkSkipRemoval(lower)
}

// isSedSkipRemoval detects sed 's/PATTERN/REPLACEMENT/' where PATTERN
// contains a skip marker but REPLACEMENT does NOT contain the same marker
// (i.e., the marker is being removed, not introduced).
func isSedSkipRemoval(cmd string) bool {
	// Extract the s/// pattern (simplified parsing)
	parts := strings.Split(cmd, "'s/")
	if len(parts) < 2 {
		return false
	}
	replacementParts := strings.Split(parts[1], "/")
	if len(replacementParts) < 2 {
		return false
	}
	pattern := strings.ToLower(replacementParts[0])
	replacement := strings.ToLower(replacementParts[1])

	hasSkipInPattern := containsAnySkipMarker(pattern)
	hasSkipInReplacement := containsAnySkipMarker(replacement)

	// Exempt if pattern has skip but replacement doesn't
	return hasSkipInPattern && !hasSkipInReplacement
}

// isAwkSkipRemoval detects awk gsub(/PATTERN/, "REPLACEMENT") where the
// pattern contains a skip marker (heuristic: assume replacement removes it).
func isAwkSkipRemoval(lower string) bool {
	if !strings.Contains(lower, "gsub(") {
		return false
	}
	// Normalize backslash escapes first (t\.Skip\( → t.skip( so escaped
	// regex markers are recognized as removal too (#588 Bug1).
	normalized := strings.ReplaceAll(lower, "\\", "")
	// Simplified: if gsub contains skip marker, assume removal
	// (awk scripts are complex, but this is a reasonable approximation)
	return containsAnySkipMarker(normalized)
}

// containsAnySkipMarker reports whether s contains any skip marker,
// case-insensitively.
func containsAnySkipMarker(s string) bool {
	for _, marker := range skipMarkers {
		if strings.Contains(s, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// hasSkipMarkersInCommands checks if any run_command/start_command input
// contains test skip/ignore markers. Read-only investigative commands
// (grep/search class, git log -S/-G) are excluded first (#544): grepping
// FOR a skip marker or investigating historical changes is legitimate,
// not gaming. Also exempts sed/awk commands that REMOVE skip markers
// (remediation, not introduction).
func hasSkipMarkersInCommands(commands []string) bool {
	for _, cmd := range commands {
		// Check if this is an exempt command
		if isSkipMarkerRemovalCommand(cmd) || isReadOnlySearchCommand(cmd) {
			continue
		}
		lower := strings.ToLower(cmd)
		for _, marker := range skipMarkers {
			// Check for both unescaped and escaped forms of skip markers
			// e.g., "t.Skip(" and "t.Skip\(" or "t\.Skip\("
			markerLower := strings.ToLower(marker)
			if strings.Contains(lower, markerLower) {
				return true
			}
			// Check escaped versions (backslash before parentheses/dots)
			// In Go raw strings, a single backslash in the pattern is represented as \\
			escapedMarker := strings.ReplaceAll(marker, "(", "\\(")
			escapedMarker = strings.ReplaceAll(escapedMarker, ".", "\\.")
			if strings.Contains(cmd, escapedMarker) {
				return true
			}
		}
	}
	return false
}

// isCIRelatedTask returns true if the user prompt suggests the task is about
// CI/build configuration itself (in which case CI edits are legitimate).
func isCIRelatedTask(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)
	// #501: the "ci" keyword must match as a whole word. As a bare substring
	// it is contained in everyday English (efficient, special, decide,
	// precision, pricing, sufficient, crucial...) and empirically matched
	// 12/12 ordinary prompts — silently disabling the CI-tampering pattern
	// for a large share of real tasks.
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, w := range words {
		if w == "ci" {
			return true
		}
	}
	ciKeywords := []string{
		"pipeline", "workflow", "github action", "gitlab ci",
		"jenkins", "circleci", "build config", "makefile", "test config",
		"pytest.ini", "conftest", "jest.config",
	}
	for _, kw := range ciKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// checkSpecGaming is the main entry point for the specification gaming
// detector. It returns a non-empty message if suspicious patterns are detected.
func (a *Agent) checkSpecGaming(stats *RunStats, userPrompt string) string {
	if a.specGaming.fired {
		return ""
	}

	var warnings []string

	// Pattern 1: Only test files edited, no source files
	testFiles := []string{}
	sourceFiles := []string{}
	for _, f := range stats.FilesEdited {
		if specGamingIsTestFile(f) {
			testFiles = append(testFiles, f)
		} else if !isConfigOrLockFile(f) {
			sourceFiles = append(sourceFiles, f)
		}
	}

	if len(testFiles) > 0 && len(sourceFiles) == 0 && !isTestWritingTask(userPrompt) {
		// Only test files were edited -- check if corresponding source exists.
		// Exempted when the task itself is to write/update tests (#544): the
		// reviewer's own run hit this while legitimately writing tests.
		warnings = append(warnings, fmt.Sprintf(
			"Only test files were modified (%s) but no source files. "+
				"Ensure you are fixing the actual code, not just modifying tests to pass.",
			strings.Join(testFiles, ", ")))
	}

	// Pattern 2: Skip markers detected in commands
	if hasSkipMarkersInCommands(stats.CommandsRun) {
		warnings = append(warnings,
			"Test skip/ignore markers detected in commands. "+
				"Skipping failing tests is not a fix -- address the root cause in source code.")
	}

	// Pattern 3: CI config tampering during non-CI tasks
	if !isCIRelatedTask(userPrompt) {
		for _, f := range stats.FilesEdited {
			if isCIConfigPath(f) {
				warnings = append(warnings, fmt.Sprintf(
					"Verification configuration file '%s' was modified during a non-CI task. "+
						"Ensure this change is necessary for the task, not to suppress test failures.", f))
				break // one warning is enough
			}
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	a.specGaming.fired = true
	debug.Log("specgaming", "specification gaming detected: %d warning(s)", len(warnings))

	msg := "[spec-gaming] Potential specification gaming detected.\n" +
		"Research shows 30%%+ of agent runs involve reward hacking (METR 2025) -- " +
		"agents gaming verification instead of solving the problem.\n\n"
	for i, w := range warnings {
		msg += fmt.Sprintf("%d. %s\n", i+1, w)
	}
	msg += "\nVerify that your changes fix the ROOT CAUSE, not just the verification signal."

	return msg
}
