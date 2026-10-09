package agent

// Expectation Declaration Protocol (TVAE, sa-140)
//
// Research basis: VeriGUI "Don't Act Blindly" (arXiv:2604.05477, ACL 2026).
// GUI agents blindly assume every action succeeded and keep generating from an
// unchanged state; repeated no-op actions cause 72.3% of execution timeouts.
// VeriGUI's fix is the TVAE closed loop: each step DECLARES the expected effect
// BEFORE acting, and the next step first verifies that the expectation was met.
// Its key incentive design is ASYMMETRIC: hallucinated success (claimed OK but
// actually failed) is penalized 4x more than false alarm, because false
// positives compound into trajectory collapse while false negatives merely add
// caution.
//
// ggcode gap (sa-140 read-only audit, verdict PARTIAL): the post-hoc detection
// chain is complete (tool_claim_verify / success_declare / premature_success /
// effect_ledger all verify AFTER the fact) and foresight_calibrate captures
// free-text predictions, but none of TVAE's three increments exist:
//   1. no STRUCTURED pre-action declaration channel (regex over prose only,
//      multi-tool turns are skipped entirely),
//   2. mismatches batch behind a threshold of 3 instead of firing on the step,
//   3. no asymmetry between "declared OK but failed" and "declared fail but OK".
//
// This file provides the pure functions; foresight_calibrate.go wires them
// into the existing prediction state machine (extension, not a parallel
// detector). An optional one-line protocol hint is injected once per run so
// models know the channel exists; models that never emit the marker are
// unaffected (legacy regex path intact).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// expectLineRe matches a single-line structured expectation declaration:
//
//	EXPECT: <check>            (attribute to the turn's single tool)
//	EXPECT: <tool> <check>     (attribute to the named tool; works in multi-tool turns)
var expectLineRe = regexp.MustCompile(`(?im)^EXPECT:\s*([a-z_0-9]+)?\s*(exit=0|exit!=0|tests_pass|>=1_match|applied>=1|success|fail)\s*$`)

// expectSideEffectTools are the tools whose outcomes the EXPECT protocol
// covers. Read-only tools rarely have a declarable postcondition beyond
// >=1_match, which is already in the whitelist.
var expectSideEffectTools = map[string]bool{
	"run_command":     true,
	"start_command":   true,
	"edit_file":       true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"write_file":      true,
	"batch_replace":   true,
	"grep":            true,
	"search_files":    true,
	"code_search":     true,
}

// expectDecl is one parsed EXPECT declaration from assistant text.
type expectDecl struct {
	toolName string // "" = attribute to the turn's single tool call
	check    string // whitelisted check token
}

// parseExpectDeclarations extracts every EXPECT marker line from assistant
// text. Deterministic, zero LLM cost.
func parseExpectDeclarations(text string) []expectDecl {
	matches := expectLineRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	decls := make([]expectDecl, 0, len(matches))
	for _, m := range matches {
		decls = append(decls, expectDecl{toolName: strings.ToLower(m[1]), check: m[2]})
	}
	return decls
}

// expectCheckPositive reports whether a check token asserts a POSITIVE outcome
// (the asymmetric branch: declared-positive-but-failed is the dangerous one).
func expectCheckPositive(check string) bool {
	switch check {
	case "exit=0", "tests_pass", ">=1_match", "applied>=1", "success":
		return true
	}
	return false // exit!=0, fail
}

// evaluateExpectCheck deterministically evaluates a declared check against the
// actual tool result. Returns (passed, humanDetail). passed=false on a
// declared-positive check that the result contradicts, and on a declared-
// negative check the result contradicts (both are mismatches; the CALLER
// applies the asymmetry).
func evaluateExpectCheck(check, resultContent string, isError bool) (passed bool, detail string) {
	structuredFail := foresightResultFailureRe.MatchString(resultContent)
	switch check {
	case "exit=0":
		if isError || structuredFail {
			return false, "declared exit=0 but the command reported failure"
		}
		return true, ""
	case "exit!=0":
		if !isError && !structuredFail {
			return false, "declared exit!=0 but the command succeeded"
		}
		return true, ""
	case "tests_pass":
		if isError || structuredFail || strings.Contains(resultContent, "--- FAIL") {
			return false, "declared tests_pass but the test run reported failures"
		}
		return true, ""
	case ">=1_match":
		if foresightResultEmpty(resultContent) {
			return false, "declared >=1_match but the result is empty or no-match"
		}
		return true, ""
	case "applied>=1":
		if isError {
			return false, "declared applied>=1 but the edit reported an error"
		}
		return true, ""
	case "success":
		if isError || structuredFail {
			return false, "declared success but the result indicates failure"
		}
		return true, ""
	case "fail":
		if !isError && !structuredFail {
			return false, "declared fail but the result succeeded"
		}
		return true, ""
	}
	return true, "" // unknown token never fires (parser whitelist makes this unreachable)
}

// expectProtocolHint is the one-time-per-run protocol announcement, injected
// when the first side-effect tool call appears so the model learns the channel.
// Empty after the first injection (and on read-only-only runs).
func (s *foresightCalibrateState) expectProtocolHint(toolCalls []provider.ToolCallDelta) string {
	if s == nil || s.protocolTaught {
		return ""
	}
	hasSideEffect := false
	for _, tc := range toolCalls {
		if expectSideEffectTools[tc.Name] {
			hasSideEffect = true
			break
		}
	}
	if !hasSideEffect {
		return ""
	}
	s.protocolTaught = true
	return fmt.Sprintf(
		"[expectation-protocol] Before calling a tool, you MAY declare its expected effect on one line: `EXPECT: <check>` (or `EXPECT: <tool> <check>` when batching several tools). Checks: %s. A declared expectation is verified against the actual result immediately; declaring success on a step that actually failed is flagged on the spot.",
		"exit=0 | exit!=0 | tests_pass | >=1_match | applied>=1 | success | fail",
	)
}
