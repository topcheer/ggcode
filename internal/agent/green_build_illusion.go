package agent

// Green Build Illusion Detector - Verification Completeness Asymmetry
//
// Research basis:
//   - "When AI Agents Touch CI/CD Configurations" (arXiv 2026): AI coding
//     agents frequently modify source code, run a build command that succeeds,
//     and declare the task complete - without ever running the test suite.
//     This is a primary reason AI-generated PRs are rejected: they compile
//     but break existing tests.
//   - "Will It Survive? Deciphering the Fate of AI-Generated Code" (2026):
//     survival analysis of 200K+ code units shows that code passing only
//     compile checks has dramatically lower long-term survival than code
//     that also passes tests.
//   - "More Code, Less Reuse" (2026): AI-agent-generated PRs have lower
//     test coverage and reviewer trust precisely because agents conflate
//     "compiles" with "works."
//   - "Stalled, Biased, and Confused" (2026): taxonomy of 16 reasoning
//     failures includes "verification substitution" - substituting a weaker
//     check (build) for a stronger one (tests).
//
// Problem: After editing source files, the agent runs a build-only command
// (e.g., "go build ./...", "npm run build", "cargo build"). The build
// succeeds, and the agent concludes the changes are correct - but it never
// ran the test suite. Compile success != correctness. Untested changes
// ship latent regressions.
//
// Observable pattern this detects:
//   Iteration 3: edit_file foo.go
//   Iteration 4: edit_file bar.go
//   Iteration 5: run_command "go build ./..."     -> success
//   Iteration 6: [agent declares "Done!" or "Complete"]
//   -> Tests were never run despite source modifications.
//
// This is distinct from:
//   - verification_debt.go: counts edits without ANY verification (build OR
//     test). This detector specifically catches the case where build WAS run
//     but tests were NOT - a more subtle and common failure.
//   - bare_edit_streak.go: tracks consecutive mutations with no intervening
//     verification at all. This detector fires even when verification happened
//     (build), because it checks the TYPE of verification.
//   - verify_scope_narrow.go: catches scope narrowing within test commands.
//     This detector catches the ABSENCE of test commands entirely.
//
// Design:
//   - Tracks the set of source files modified since last test run
//   - Tracks whether a build-only command has been run
//   - Tracks whether a test command has been run
//   - Fires when: (source modified) AND (build run, succeeded) AND
//     (no test run) AND (agent signals completion)
//   - Zero LLM cost - pure deterministic state tracking
//   - Fires at most once per run (advisory, non-blocking)

import (
	"regexp"
	"strings"
)

type greenBuildIllusionState struct {
	modifiedFiles      map[string]bool // source files modified since last test run
	buildRunSucceeded  bool            // a build-only command succeeded
	testRun            bool            // a test command was run (any outcome)
	completionSignaled bool            // agent signaled task completion
	fired              bool            // already fired this run
}

func newGreenBuildIllusionState() *greenBuildIllusionState {
	return &greenBuildIllusionState{
		modifiedFiles: make(map[string]bool),
	}
}

func (g *greenBuildIllusionState) reset() {
	g.modifiedFiles = make(map[string]bool)
	g.buildRunSucceeded = false
	g.testRun = false
	g.completionSignaled = false
	g.fired = false
}

// Source file extensions that represent production code (not test/generated).
var greenBuildSourceExts = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".py": true, ".rs": true, ".java": true, ".c": true, ".cpp": true,
	".h": true, ".hpp": true, ".rb": true, ".kt": true, ".swift": true,
}

// Test file detection: files that are themselves tests.
func gbiIsTestFile(path string) bool {
	lower := strings.ToLower(path)
	// Common test file patterns
	if strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".spec.js") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, ".spec.tsx") ||
		strings.HasSuffix(lower, ".test.jsx") ||
		strings.HasSuffix(lower, ".spec.jsx") ||
		strings.HasSuffix(lower, "test.py") ||
		strings.HasSuffix(lower, "_test.py") ||
		strings.HasSuffix(lower, "_test.rs") ||
		strings.HasSuffix(lower, "test_.java") ||
		strings.HasSuffix(lower, "_test.py") ||
		strings.HasSuffix(lower, "test.rb") ||
		strings.HasSuffix(lower, "_test.rb") {
		return true
	}
	// Directory-based test detection
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/") {
		return true
	}
	// Python/Javascript test prefix convention: test_foo.py, test_foo.js
	if strings.HasPrefix(lower, "test_") || strings.Contains(lower, "/test_") {
		return true
	}
	return false
}

func isSourceFile(path string) bool {
	if gbiIsTestFile(path) {
		return false
	}
	for ext := range greenBuildSourceExts {
		if strings.HasSuffix(strings.ToLower(path), ext) {
			return true
		}
	}
	return false
}

// Build-only command patterns (compile without testing).
var greenBuildBuildOnlyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bgo\s+build\b`),
	regexp.MustCompile(`(?i)\bgo\s+vet\b`), // vet is not a test
	regexp.MustCompile(`(?i)\bnpm\s+run\s+build\b`),
	regexp.MustCompile(`(?i)\bnpm\s+run\s+compile\b`),
	regexp.MustCompile(`(?i)\byarn\s+build\b`),
	regexp.MustCompile(`(?i)\bpnpm\s+build\b`),
	regexp.MustCompile(`(?i)\bcargo\s+build\b`),
	regexp.MustCompile(`(?i)\bcargo\s+check\b`),
	regexp.MustCompile(`(?i)\bmake\s+\w*build\w*\b`), // make build, make compile
	regexp.MustCompile(`(?i)\bjavac\b`),
	regexp.MustCompile(`(?i)\bgcc\b`),
	regexp.MustCompile(`(?i)\bg\+\+\b`),
	regexp.MustCompile(`(?i)\btsc\b`), // TypeScript compiler
	regexp.MustCompile(`(?i)\bdotnet\s+build\b`),
	regexp.MustCompile(`(?i)\bdotnet\s+publish\b`),
	regexp.MustCompile(`(?i)\bwebpack\b`),
}

// Test command patterns (run tests).
var greenBuildTestPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bgo\s+test\b`),
	regexp.MustCompile(`(?i)\bnpm\s+test\b`),
	regexp.MustCompile(`(?i)\bnpm\s+run\s+test\b`),
	regexp.MustCompile(`(?i)\byarn\s+test\b`),
	regexp.MustCompile(`(?i)\bpnpm\s+test\b`),
	regexp.MustCompile(`(?i)\bcargo\s+test\b`),
	regexp.MustCompile(`(?i)\bmake\s+test\b`),
	regexp.MustCompile(`(?i)\bdotnet\s+test\b`),
	regexp.MustCompile(`(?i)\bpython\s+.*pytest\b`),
	regexp.MustCompile(`(?i)\bpython\s+.*unittest\b`),
	regexp.MustCompile(`(?i)\bpytest\b`),
	regexp.MustCompile(`(?i)\bjest\b`),
	regexp.MustCompile(`(?i)\bvitest\b`),
	regexp.MustCompile(`(?i)\bmocha\b`),
	regexp.MustCompile(`(?i)\brake\s+test\b`),
	regexp.MustCompile(`(?i)\bgradle\s+test\b`),
	regexp.MustCompile(`(?i)\bmvn\s+test\b`),
}

func isBuildOnlyCommand(cmd string) bool {
	for _, bp := range greenBuildBuildOnlyPatterns {
		if bp.MatchString(cmd) {
			// Ensure it's not ALSO a test command
			if !isTestCommand(cmd) {
				return true
			}
		}
	}
	return false
}

func isTestCommand(cmd string) bool {
	for _, tp := range greenBuildTestPatterns {
		if tp.MatchString(cmd) {
			return true
		}
	}
	return false
}

// Completion signal patterns in assistant text.
var greenBuildCompletionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(done|complete|completed|finished|all set|that's it|task is complete|i'm finished|implementation is complete)\b[.!]?\s*$`),
	regexp.MustCompile(`(?i)\bchanges?\s+(are\s+)?(complete|done|finished)\b`),
	regexp.MustCompile(`(?i)\bthe\s+(task|implementation|feature|fix)\s+is\s+(complete|done|finished)\b`),
}

func gbiHasCompletionSignal(text string) bool {
	for _, cp := range greenBuildCompletionPatterns {
		if cp.MatchString(text) {
			return true
		}
	}
	return false
}

// recordToolCall records tool activity relevant to the detector.
func (g *greenBuildIllusionState) recordToolCall(toolName string, args string, isError bool) {
	if g.fired {
		return
	}

	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit":
		path := extractGBIFilePath(args)
		if path != "" && isSourceFile(path) {
			g.modifiedFiles[path] = true
		}

	case "run_command":
		if isTestCommand(args) {
			g.testRun = true
			// Tests were run - reset modified tracking since they're verified
			g.modifiedFiles = make(map[string]bool)
			g.buildRunSucceeded = false
		} else if isBuildOnlyCommand(args) && !isError {
			// New build after tests were run invalidates prior test coverage
			if g.testRun {
				g.testRun = false
				// Re-add tracked files if any were modified before the test
			}
			g.buildRunSucceeded = true
		}
	}
}

// recordAssistantText checks for completion signals.
func (g *greenBuildIllusionState) recordAssistantText(text string) {
	if g.fired {
		return
	}
	if gbiHasCompletionSignal(text) {
		g.completionSignaled = true
	}
}

// maybeWarn returns guidance if the green build illusion pattern is detected.
// Fires when: source files were modified, a build succeeded, no test was run,
// and the agent signaled completion.
func (g *greenBuildIllusionState) maybeWarn() string {
	if g.fired {
		return ""
	}
	if !g.completionSignaled {
		return ""
	}
	if len(g.modifiedFiles) == 0 {
		return ""
	}
	if g.testRun {
		return ""
	}
	if !g.buildRunSucceeded {
		return ""
	}

	g.fired = true
	fileList := formatFileList(g.modifiedFiles, 5)
	return "[Green Build Illusion] Source files were modified and a build-only command " +
		"succeeded, but no test command was run before declaring completion. " +
		"Compile success does not guarantee correctness - existing tests may now fail. " +
		"Modified files: " + fileList + ". Run the test suite before concluding."
}

// extractGBIFilePath extracts file paths from tool arguments JSON.
func extractGBIFilePath(args string) string {
	// Look for file_path or path field in the JSON-like args
	pathRe := regexp.MustCompile(`"file_path"\s*:\s*"([^"]+)"`)
	if m := pathRe.FindStringSubmatch(args); len(m) > 1 {
		return m[1]
	}
	path2Re := regexp.MustCompile(`"path"\s*:\s*"([^"]+)"`)
	if m := path2Re.FindStringSubmatch(args); len(m) > 1 {
		return m[1]
	}
	return ""
}

func formatFileList(files map[string]bool, maxShow int) string {
	var list []string
	count := 0
	for fn := range files {
		if count >= maxShow {
			list = append(list, "...")
			break
		}
		list = append(list, fn)
		count++
	}
	return strings.Join(list, ", ")
}
