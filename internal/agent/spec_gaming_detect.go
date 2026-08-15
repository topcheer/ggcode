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
	configSuffixes := []string{
		".toml", ".yaml", ".yml", ".json", ".ini", ".cfg",
		".lock", ".mod", ".sum", ".env",
		".md", ".txt", ".lock",
		"dockerfile", "makefile",
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

// hasSkipMarkersInCommands checks if any run_command/start_command input
// contains test skip/ignore markers.
func hasSkipMarkersInCommands(commands []string) bool {
	for _, cmd := range commands {
		lower := strings.ToLower(cmd)
		for _, marker := range skipMarkers {
			if strings.Contains(lower, strings.ToLower(marker)) {
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

	if len(testFiles) > 0 && len(sourceFiles) == 0 {
		// Only test files were edited -- check if corresponding source exists
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
