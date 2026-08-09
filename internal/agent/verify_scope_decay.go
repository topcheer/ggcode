package agent

// Verification Scope Decay Detector
//
// Research basis:
//   - Harasimowicz "AI Agent Failure Modes & Taxonomy" (2026): identifies
//     "Verification Erosion" as a silent-at-scale failure where agents
//     progressively reduce verification rigor across multi-step runs.
//   - "A Self-Improving Coding Agent" (arXiv:2504.15228, NeurIPS 2025):
//     demonstrates that verification scope patterns are a key differentiator
//     between successful and failed agent runs. Successful runs maintain or
//     expand verification scope, while failing runs narrow it.
//   - SWE-bench analysis (2025): shows that agents that switch from broad
//     test suites to narrow/specific checks mid-run have ~2x higher rate of
//     incomplete fixes.
//
// Problem: AI coding agents exhibit a subtle degradation pattern over
// multi-step runs. They start with comprehensive verification ("run full
// test suite"), but progressively narrow the scope:
//
//   Iteration 1: "go test ./..."          (FULL - all packages)
//   Iteration 3: "go test ./internal/..." (PARTIAL - one package)
//   Iteration 5: "go test -run TestFoo"   (MINIMAL - one test)
//   Iteration 7: "go build file.go"       (MINIMAL - compile only)
//   Iteration 9: (no verification)        (NONE - just edits)
//
// This "verification scope decay" means the final state is validated far
// less rigorously than the initial state, even though it contains MORE
// changes. The user sees edits "verified" early and assumes the same rigor
// continued, but it silently eroded.
//
// Distinction from existing detectors:
//   - phantom_verify.go: checks if verification was CLAIMED but not RUN.
//     This detector checks the TRAJECTORY of verification scope over time.
//   - verify_disconnect.go: checks advancement past FAILURES. This checks
//     scope reduction even when everything "passes."
//   - temporal_blindness.go: checks stale verification after mutations.
//     This checks scope narrowing independent of mutations.
//   - bareEditStreak: checks consecutive edits without ANY verification.
//     This checks the QUALITY/scope of verification that IS run.
//
// Detection approach:
//   1. Classify each verification tool call's scope: FULL, PARTIAL, MINIMAL.
//   2. Track the sequence of scope levels across the run.
//   3. If scope consistently trends downward (monotonic decrease across
//      3+ verification calls, or a FULL->MINIMAL jump), inject guidance.
//
// Design:
//   - Zero LLM cost - pure deterministic classification.
//   - Non-blocking advisory hint, capped at 1 injection per run.
//   - Only triggers after 3+ verification calls (enough data for a trend).
//   - Ignores scope increases (those are positive signals).

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	vsdMaxWarnings = 1

	// vsdMinVerifications is the minimum number of verification calls needed
	// to establish a trend. Below this, we don't have enough signal.
	vsdMinVerifications = 3

	// vsdScopeLevel constants represent verification breadth.
	vsdScopeFull    = 3 // all tests / full build / full lint
	vsdScopePartial = 2 // specific package / specific test file
	vsdScopeMinimal = 1 // single test / compile-only / single file
)

// vsdScopeName maps scope levels to human-readable names.
var vsdScopeNames = map[int]string{
	vsdScopeFull:    "full test suite / full build",
	vsdScopePartial: "specific package / subset",
	vsdScopeMinimal: "single test / compile-only",
}

// vsdVerification is a recorded verification tool call with its scope.
type vsdVerification struct {
	toolName  string
	command   string
	scope     int
	iteration int
}

// verifyScopeDecayState tracks verification calls and detects scope decay.
type verifyScopeDecayState struct {
	warnings      int
	verifications []vsdVerification
}

func newVerifyScopeDecayState() *verifyScopeDecayState {
	return &verifyScopeDecayState{}
}

func (s *verifyScopeDecayState) reset() {
	s.warnings = 0
	s.verifications = nil
}

// --- Verification tool identification ---

// vsdVerificationTools are tool names that run verification commands.
var vsdVerificationTools = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

// --- Command classification patterns ---

// vsdFullTestRe matches full-suite test commands (all packages/tests).
// "go test ./..." and "go test ." must match, but "go test ./internal/..." must not.
var vsdFullTestRe = regexp.MustCompile(`(?i)go\s+test\s+(\./\.\.\.|\.)\s*$|go\s+test\s+(\./\.\.\.|\.)\s+[-]`)

// vsdFullBuildRe matches full-project build commands.
var vsdFullBuildRe = regexp.MustCompile(`(?i)go\s+build\s+(\./\.\.\.|\.)\s*$|go\s+build\s+(\./\.\.\.|\.)\s+[-]|make\s+(build|compile)|cargo\s+build\s+--release|npm\s+run\s+build`)

// vsdFullLintRe matches full-project lint/typecheck commands.
var vsdFullLintRe = regexp.MustCompile(`(?i)go\s+vet\s+(\./\.\.\.|\.)\s*$|go\s+vet\s+(\./\.\.\.|\.)\s+[-]|golangci-lint\s+run|mypy\s+\.|eslint\s+\.|tsc\s+--noEmit|make\s+lint`)

// vsdFullTestOtherRe matches non-go full-suite commands (bare pytest with no path).
var vsdFullTestOtherRe = regexp.MustCompile(`(?i)(^|[^/\w])(npm\s+test|maven\s+test|mvn\s+test|pytest\s*$|cargo\s+test\s*$|make\s+(test|verify|check|ci))`)

// vsdPartialTestRe matches package/subset test commands.
// Must come after FULL check. Matches explicit subpaths.
var vsdPartialTestRe = regexp.MustCompile(`(?i)(go\s+test\s+\.\./|go\s+test\s+\./\w|go\s+test\s+\w+[\./]|pytest\s+\S|cargo\s+test\s+--)`)

// vsdMinimalTestRe matches single-test or single-file verification.
var vsdMinimalTestRe = regexp.MustCompile(`(?i)(go\s+test\s+(-run|-run=)\s*\S|go\s+test\s+\S+\.go|go\s+build\s+\S+\.go|go\s+vet\s+\S+\.go)`)

// vsdCompileOnlyRe matches compile-only checks (no test execution).
var vsdCompileOnlyRe = regexp.MustCompile(`(?i)(go\s+build\s|go\s+vet\s|tsc\s|cargo\s+check|rustc\s)`)

// classifyVerificationScope determines the breadth of a verification command.
// Returns (scope, true) if this is a verification command, (0, false) otherwise.
// Checks PARTIAL before FULL because "go test ./internal/..." contains the
// substring "./..." which would also match the FULL pattern.
func classifyVerificationScope(command string) (int, bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return 0, false
	}

	// Check full-scope first (exact "./..." or "." target, or make test)
	if vsdFullTestRe.MatchString(cmd) || vsdFullBuildRe.MatchString(cmd) ||
		vsdFullLintRe.MatchString(cmd) || vsdFullTestOtherRe.MatchString(cmd) {
		return vsdScopeFull, true
	}

	// Check partial scope (explicit subpaths like ./internal/...)
	if vsdPartialTestRe.MatchString(cmd) {
		return vsdScopePartial, true
	}

	// Check minimal scope
	if vsdMinimalTestRe.MatchString(cmd) {
		return vsdScopeMinimal, true
	}

	// Compile-only (build/vet on specific path) is minimal
	if vsdCompileOnlyRe.MatchString(cmd) {
		return vsdScopeMinimal, true
	}

	return 0, false
}

// recordVerification classifies and stores a verification tool call.
func (s *verifyScopeDecayState) recordVerification(toolName, command string, iteration int) {
	if !vsdVerificationTools[toolName] {
		return
	}
	scope, ok := classifyVerificationScope(command)
	if !ok {
		return
	}
	s.verifications = append(s.verifications, vsdVerification{
		toolName:  toolName,
		command:   command,
		scope:     scope,
		iteration: iteration,
	})
}

// maybeWarnScopeDecay checks if verification scope has been decaying across
// the run and returns a guidance hint if so.
func (s *verifyScopeDecayState) maybeWarnScopeDecay() string {
	if s.warnings >= vsdMaxWarnings || len(s.verifications) < vsdMinVerifications {
		return ""
	}

	if s.detectNonIncreasingTrend() || s.detectFullToMinimalJump() || s.detectOverallDecay() {
		s.warnings++
		return s.buildDecayWarning()
	}
	return ""
}

// detectNonIncreasingTrend checks if scope never increases and has at least
// one strict decrease across all verifications.
func (s *verifyScopeDecayState) detectNonIncreasingTrend() bool {
	hasDecrease := false
	for i := 1; i < len(s.verifications); i++ {
		if s.verifications[i].scope > s.verifications[i-1].scope {
			return false
		}
		if s.verifications[i].scope < s.verifications[i-1].scope {
			hasDecrease = true
		}
	}
	return hasDecrease
}

// detectFullToMinimalJump checks for a 2-level drop in a single step.
func (s *verifyScopeDecayState) detectFullToMinimalJump() bool {
	for i := 1; i < len(s.verifications); i++ {
		if s.verifications[i-1].scope == vsdScopeFull &&
			s.verifications[i].scope == vsdScopeMinimal {
			return true
		}
	}
	return false
}

// detectOverallDecay checks for FULL->MINIMAL across non-monotonic sequences
// with at least 2 intermediate narrower steps.
func (s *verifyScopeDecayState) detectOverallDecay() bool {
	first := s.verifications[0].scope
	last := s.verifications[len(s.verifications)-1].scope
	if first != vsdScopeFull || last != vsdScopeMinimal {
		return false
	}
	narrower := 0
	for _, v := range s.verifications[1:] {
		if v.scope < first {
			narrower++
		}
	}
	return narrower >= 2
}

// buildDecayWarning constructs the guidance message.
func (s *verifyScopeDecayState) buildDecayWarning() string {
	first := s.verifications[0]
	last := s.verifications[len(s.verifications)-1]
	return fmt.Sprintf(
		"[verification-scope-decay] Your verification scope has narrowed from "+
			"%s (iteration %d) to %s (iteration %d). "+
			"The final state contains more changes but is validated less rigorously. "+
			"Re-run the full test suite or full build to confirm the complete change set.",
		vsdScopeNames[first.scope], first.iteration,
		vsdScopeNames[last.scope], last.iteration,
	)
}
