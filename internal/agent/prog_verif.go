package agent

import (
	"strings"
)

// Progressive Verification (SA-109)
//
// Research: ReVeal (arXiv:2506.11442) and GoalAct show that verifying incrementally
// during execution improves success rates by 12%+. Instead of verifying only after
// file writes, we add lightweight checkpoints after critical tool results to fail fast.
//
// This is NOT a detector - it's a passive verification layer that injects guidance
// when it detects early signs of failure. Zero LLM cost, deterministic.

const (
	maxProgVerifWarnings = 2
)

// progVerifTracker monitors progressive verification state across a run.
type progVerifTracker struct {
	fires      int
	lastTool   string
	checkpoint string
}

func (t *progVerifTracker) reset() {
	t.fires = 0
	t.lastTool = ""
	t.checkpoint = ""
}

// checkAfterToolResult runs progressive verification after tool execution.
// Returns guidance if verification fails, empty string otherwise.
func (t *progVerifTracker) checkAfterToolResult(toolName string, result string, isError bool) string {
	if t.fires >= maxProgVerifWarnings {
		return ""
	}

	t.lastTool = toolName

	// Check 1: Build/test failures need immediate feedback
	if isError && isBuildTestTool(toolName) {
		t.fires++
		t.checkpoint = toolName
		return "[Progressive Verification] " + buildTestFailureGuidance(result)
	}

	// Check 2: Edit without subsequent verify/build is risky
	if isEditTool(toolName) && !strings.Contains(result, "error") {
		if t.lastTool != "" && !isVerifyTool(t.lastTool) {
			// Don't warn on first edit - wait for pattern
			if t.checkpoint == "edit_no_verify" {
				t.fires++
				t.checkpoint = "edit_no_verify"
				return "[Progressive Verification] Multiple file edits without verification (build/test/lint). Consider running a build or test to catch issues early."
			}
			t.checkpoint = "edit_no_verify"
		}
	}

	return ""
}

// buildTestFailureGuidance provides specific guidance for build/test failures.
func buildTestFailureGuidance(result string) string {
	lower := strings.ToLower(result)

	// Common Go build/test errors
	if strings.Contains(lower, "undefined:") || strings.Contains(lower, "not declared") {
		return "Build failed with undefined symbol. Check for typos, missing imports, or if you renamed a type/function without updating all references."
	}
	if strings.Contains(lower, "cannot use") || strings.Contains(lower, "type mismatch") {
		return "Type mismatch error. Verify the expected type and consider using type assertions or conversions if needed."
	}
	if strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") {
		return "File or package not found. Check file paths and import statements. Did you move or delete a file?"
	}
	if strings.Contains(lower, "expected") && strings.Contains(lower, "got") {
		return "Test assertion failed. Review the test expectation vs actual output. The logic or test case may need adjustment."
	}

	// Generic fallback
	return "Build/test execution failed. Review the error output above, fix the root cause, and re-run."
}

// isBuildTestTool checks if a tool is a build/test tool (for progVerif).
func isBuildTestTool(name string) bool {
	return strings.Contains(name, "run_command") || strings.Contains(name, "start_command")
}
