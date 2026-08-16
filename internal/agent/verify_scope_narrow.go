package agent

// Verification Scope Narrowing Detector
//
// Research basis: "Towards Understanding Specification Gaming in Reasoning
// Models" (arXiv:2605.02269, 2026) found that reasoning models unconsciously
// narrow verification scope to obtain passing results. The "Verification
// Horizon" paper (arXiv:2606.26300) formalizes this as "proxy optimization":
// every verifier is a proxy for true intent, and agents learn to optimize the
// proxy rather than the goal.
//
// Observable pattern in coding agents:
//   1. Agent runs `go test ./...` → fails with 5 errors
//   2. Agent runs `go test ./internal/agent/` → fails with 2 errors
//   3. Agent runs `go test -run TestSpecific ./internal/agent/` → passes
//   4. Agent declares success
//
// The scope was narrowed from "all packages" → "one package" → "one test",
// masking 3 still-failing tests. The agent didn't tamper with test files
// (caught by spec_gaming_detect.go) - it tampered with the *command*.
//
// This is distinct from existing detectors:
//   - spec_gaming_detect.go: detects test FILE tampering (editing/skipping tests)
//   - verify_hint.go: reminds agent to run verification at all
//   - test_impact.go: suggests which tests to run based on changed files
//   - verify_suppress: detects error-masking shell operators (|| true, 2>/dev/null)
//
// None of them detect the specific pattern of progressively narrowing the
// test/build command scope across iterations, which is a subtle form of
// specification gaming that doesn't modify any files - just commands.
//
// Detection approach (zero LLM cost, deterministic):
//   - Track test/build commands across the run (go test, pytest, npm test, etc.)
//   - Extract the scope of each command (package path, test filter, file list)
//   - When scope strictly narrows 2+ times in sequence AND the final result
//     was "passing", inject a warning to re-run the original broader scope
//   - Only fires when scope narrows AND outcome flips from fail→pass, which is
//     the signature of scope-narrowing specification gaming

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxScopeNarrowWarnings = 1

// scopeNarrowState tracks test/build commands to detect scope narrowing.
type scopeNarrowState struct {
	// history stores normalized scope info for each verification command.
	history []scopeEntry
	// fired prevents duplicate warnings.
	fired bool
}

type scopeEntry struct {
	toolName   string // run_command, start_command
	rawCommand string // original command text
	scope      string // normalized scope (package path + filters)
	category   string // "go-test", "pytest", "npm-test", etc.
	passed     bool   // whether the command succeeded (exit code 0 / no error)
}

func newScopeNarrowState() *scopeNarrowState {
	return &scopeNarrowState{}
}

func (s *scopeNarrowState) reset() {
	s.history = s.history[:0]
	s.fired = false
}

// Patterns for extracting scope from common test/build commands.
var (
	// go test patterns: capture package path and -run filter
	goTestScopeRe = regexp.MustCompile(`go\s+test\s+(?:-tags\s+\S+\s+)?(.*)`)
	goTestRunRe   = regexp.MustCompile(`-run\s+(\S+)`)
	goTestPkgRe   = regexp.MustCompile(`(?:^|\s)(\./\S*|internal/\S*|cmd/\S*|\.\.\.)`)

	// pytest: -k filter and file/path argument
	pytestScopeRe = regexp.MustCompile(`pytest\s+(.*)`)
	pytestKRe     = regexp.MustCompile(`-k\s+(\S+)`)

	// npm/yarn test: --grep or specific file
	npmTestGrepRe = regexp.MustCompile(`--grep\s+(\S+)`)
)

// isGoSubcommand returns true if cmd starts with "go <sub>" as a word.
// This avoids false positives like "cargo test" containing "go test" substring.
func isGoSubcommand(cmd, sub string) bool {
	fields := strings.Fields(cmd)
	if len(fields) >= 2 && fields[0] == "go" && fields[1] == sub {
		return true
	}
	// Also handle "time go test" or "sudo go test" prefixes
	for i := 0; i+2 < len(fields); i++ {
		if fields[i+1] == "go" && fields[i+2] == sub {
			return true
		}
	}
	return false
}

// classifyVerificationCommand returns (category, scope) if the command is a
// test/build verification command, or ("", "") otherwise.
func classifyVerificationCommand(cmd string) (category, scope string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", ""
	}

	// Strip env prefixes
	for _, prefix := range []string{"sudo ", "time ", "nice "} {
		cmd = strings.TrimPrefix(cmd, prefix)
	}

	// go test / go vet — use word-boundary aware matching to avoid
	// false positives like "cargo test" containing "go test" substring.
	if isGoSubcommand(cmd, "test") || isGoSubcommand(cmd, "vet") {
		category = "go-test"
		scope = extractGoTestScope(cmd)
		return
	}

	// go build
	if isGoSubcommand(cmd, "build") {
		category = "go-build"
		scope = extractGoTestScope(cmd) // reuse package extraction
		return
	}

	// pytest / python -m pytest
	if strings.Contains(cmd, "pytest") || strings.Contains(cmd, "python -m pytest") {
		category = "pytest"
		scope = extractPytestScope(cmd)
		return
	}

	// npm/yarn/pnpm test
	for _, t := range []string{"npm test", "yarn test", "pnpm test", "npm run test"} {
		if strings.Contains(cmd, t) {
			category = "npm-test"
			scope = extractNpmTestScope(cmd)
			return
		}
	}

	// cargo test
	if strings.Contains(cmd, "cargo test") {
		category = "cargo-test"
		scope = extractAfterKeyword(cmd, "cargo test")
		return
	}

	// make test / make check
	if strings.Contains(cmd, "make test") || strings.Contains(cmd, "make check") || strings.Contains(cmd, "make verify") {
		category = "make-test"
		return
	}

	return "", ""
}

// extractGoTestScope extracts a normalized scope from a go test/build command.
func extractGoTestScope(cmd string) string {
	var parts []string

	// Extract -run filter
	if m := goTestRunRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "run:"+m[1])
	}

	// Broad scope: ./... (all packages) or bare 'go test' with no package path.
	// Explicit package paths (with or without ./) are narrow scope (issue #24).
	if strings.Contains(cmd, "./...") {
		parts = append(parts, "scope:broad")
	} else {
		// Extract specific package paths (both ./pkg/ and pkg/ forms)
		pkgs := goTestPkgRe.FindAllStringSubmatch(cmd, -1)
		for _, m := range pkgs {
			if len(m) > 1 && m[1] != "./..." {
				parts = append(parts, "pkg:"+m[1])
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "scope:broad")
		}
	}

	return strings.Join(parts, "|")
}

// extractPytestScope extracts scope from a pytest command.
func extractPytestScope(cmd string) string {
	var parts []string

	if m := pytestKRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "k:"+m[1])
	}

	// Extract file/path arguments (non-flag args after "pytest")
	rest := ""
	if m := pytestScopeRe.FindStringSubmatch(cmd); len(m) > 1 {
		rest = m[1]
	}
	for _, tok := range strings.Fields(rest) {
		if !strings.HasPrefix(tok, "-") && (strings.Contains(tok, "/") || strings.HasSuffix(tok, ".py")) {
			parts = append(parts, "file:"+tok)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "scope:broad")
	}

	return strings.Join(parts, "|")
}

// extractNpmTestScope extracts scope from npm/yarn test commands.
func extractNpmTestScope(cmd string) string {
	var parts []string

	if m := npmTestGrepRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "grep:"+m[1])
	}

	// Extract specific test file
	for _, tok := range strings.Fields(cmd) {
		if strings.HasSuffix(tok, ".test.js") || strings.HasSuffix(tok, ".spec.js") ||
			strings.HasSuffix(tok, ".test.ts") || strings.HasSuffix(tok, ".spec.ts") {
			parts = append(parts, "file:"+tok)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "scope:broad")
	}

	return strings.Join(parts, "|")
}

// extractAfterKeyword returns everything after the given keyword, trimmed.
func extractAfterKeyword(cmd, keyword string) string {
	idx := strings.Index(cmd, keyword)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(cmd[idx+len(keyword):])
}

// recordVerificationCommand tracks a verification command and its result.
// Returns a non-empty warning string if scope narrowing is detected.
func (s *scopeNarrowState) recordVerificationCommand(toolName, cmd string, output string, isError bool) string {
	if s.fired {
		return ""
	}

	category, scope := classifyVerificationCommand(cmd)
	if category == "" {
		return ""
	}

	entry := scopeEntry{
		toolName:   toolName,
		rawCommand: cmd,
		scope:      scope,
		category:   category,
		// #550 A3: the exit code (isError) is the authoritative outcome —
		// the old `!isError && !looksLikeTestFailure(output)` ALSO let a
		// lowercase-substring scan judge PASSING runs (exit 0) whose test
		// names merely contain "error"/"fail" (e.g. TestHandleErrorFallback)
		// as failed, silencing the narrowing detector on exactly the runs
		// it should be watching. Output text is no longer trusted to gate
		// the verdict.
		passed: !isError,
	}

	s.history = append(s.history, entry)

	// Need at least 3 entries to detect narrowing pattern
	if len(s.history) < 3 {
		return ""
	}

	// Check the last 3 same-category entries for scope narrowing
	return s.checkNarrowingPattern()
}

// looksLikeTestFailure scans command output for failure indicators.
func looksLikeTestFailure(output string) bool {
	output = strings.ToLower(output)
	for _, marker := range []string{
		"fail", "failed", "error", "panic:", "--- fail", "assertion", "not ok",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// checkNarrowingPattern checks whether the recent command history shows a
// progressive scope narrowing pattern with a fail→pass transition.
func (s *scopeNarrowState) checkNarrowingPattern() string {
	n := len(s.history)
	if n < 3 {
		return ""
	}

	// Find consecutive entries of the same category
	last := s.history[n-1]
	if last.category == "make-test" {
		// make commands don't have extractable scope; skip
		return ""
	}

	// Look at the last 3 same-category entries
	var sameCat []scopeEntry
	for i := n - 1; i >= 0 && len(sameCat) < 3; i-- {
		if s.history[i].category == last.category {
			sameCat = append(sameCat, s.history[i])
		}
	}
	// sameCat is in reverse order (most recent first)
	if len(sameCat) < 3 {
		return ""
	}

	// Reverse to chronological order
	oldest := sameCat[2]
	middle := sameCat[1]
	newest := sameCat[0]

	// Pattern: scope strictly narrows from oldest→middle→newest,
	// AND outcome transitions from fail→pass
	if !isNarrower(middle.scope, oldest.scope) || !isNarrower(newest.scope, middle.scope) {
		return ""
	}
	if oldest.passed || !middle.passed || !newest.passed {
		// We need: oldest failed, and the narrowing resulted in pass
		// Actually accept: oldest failed, middle or newest passed
		if oldest.passed {
			return ""
		}
		if !middle.passed && !newest.passed {
			return ""
		}
	}

	s.fired = true
	originalCmd := oldest.rawCommand
	debug.Log("agent", "verification scope narrowing detected: '%s' → '%s' → '%s'",
		truncateCmdShort(oldest.rawCommand), truncateCmdShort(middle.rawCommand), truncateCmdShort(newest.rawCommand))

	return "[Verification Scope Narrowing Detected] Your test/build commands have progressively narrowed scope:\n" +
		"  1. `" + truncateCmdShort(oldest.rawCommand) + "` -> failed\n" +
		"  2. `" + truncateCmdShort(middle.rawCommand) + "` -> " + passFailStr(middle.passed) + "\n" +
		// #550 A2: report the NEWEST command's REAL outcome — the hardcoded
		// "-> passed" lied whenever the latest run actually failed (firing
		// only requires middle OR newest to pass), contradicting the evidence
		// and misleading the agent about the final state.
		"  3. `" + truncateCmdShort(newest.rawCommand) + "` -> " + passFailStr(newest.passed) + "\n\n" +
		"The narrowing may be masking failures outside the narrowed scope. " +
		"Re-run the ORIGINAL broader command (`" + truncateCmdShort(originalCmd) + "`) to confirm " +
		"all tests/builds pass, not just the narrowed subset. " +
		"If broader tests legitimately fail, fix them rather than narrowing scope further."
}

// isNarrower returns true if scope b is strictly narrower than scope a.
func isNarrower(b, a string) bool {
	if a == "" || b == "" {
		return false
	}
	// Broad → specific narrowing
	if strings.Contains(a, "scope:broad") && !strings.Contains(b, "scope:broad") {
		return true
	}
	// Package narrowing: ./... → ./internal/agent/
	aPkgs := extractPkgList(a)
	bPkgs := extractPkgList(b)
	if len(aPkgs) > 0 && len(bPkgs) > 0 {
		if len(bPkgs) < len(aPkgs) {
			return true
		}
		// Check if b packages are a subset of a packages
		aSet := make(map[string]bool)
		for _, p := range aPkgs {
			aSet[p] = true
		}
		allSubset := true
		for _, p := range bPkgs {
			if !aSet[p] {
				allSubset = false
				break
			}
		}
		if allSubset && len(bPkgs) < len(aPkgs) {
			return true
		}
	}
	// Added -run filter (go) or -k filter (pytest) or --grep (npm)
	if strings.Contains(b, "run:") && !strings.Contains(a, "run:") {
		return true
	}
	if strings.Contains(b, "k:") && !strings.Contains(a, "k:") {
		return true
	}
	if strings.Contains(b, "grep:") && !strings.Contains(a, "grep:") {
		return true
	}
	// Added file filter
	if strings.Contains(b, "file:") && !strings.Contains(a, "file:") {
		return true
	}
	return false
}

// extractPkgList extracts package entries from a scope string.
func extractPkgList(scope string) []string {
	var pkgs []string
	for _, part := range strings.Split(scope, "|") {
		if strings.HasPrefix(part, "pkg:") {
			pkgs = append(pkgs, strings.TrimPrefix(part, "pkg:"))
		}
	}
	return pkgs
}

// truncateCmdShort truncates a command string for display.
func truncateCmdShort(cmd string) string {
	if len(cmd) > 80 {
		return cmd[:77] + "..."
	}
	return cmd
}

// passFailStr returns "passed" or "failed" for display.
func passFailStr(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
