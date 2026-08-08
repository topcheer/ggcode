package agent

// Narrative-Evidence Decoupling Detector
//
// Research basis:
//   - "Evaluating Agentic AI in the Wild" (arXiv:2605.01604, 2026): identifies
//     "Explanation-Decision Decoupling" as a silent-at-scale failure mode where a
//     correct-looking decision is paired with a fabricated rationale pointing at the
//     wrong causal signal. The sharpest audit-trail threat: decision logs look clean
//     while the explanation is fabricated.
//   - "AI Agent Failure Modes & Taxonomy" (Harasimowicz, 2026): classifies this as
//     FC-7 "Coherence Illusion" - early errors propagate, accumulating derived
//     evidence that makes the output look coherent while being systematically wrong.
//   - MAST (NeurIPS 2025, arXiv:2503.13657): specification failures account for
//     41.8% of multi-agent failure traces, many driven by agents asserting facts
//     contradicted by system logs.
//   - "Beyond Task Completion" (arXiv:2603.03116, 2026): demonstrates "corrupted
//     success" where agents reach a terminal state via fabricated confirmations,
//     scoring identically to correct runs in outcome-only benchmarks.
//
// Problem: AI coding agents sometimes produce assistant text that DIRECTLY
// contradicts the most recent tool output. Examples:
//   - Tool returns "Error: 3 tests failed" → agent says "All tests pass!"
//   - Tool returns "undefined: foo" → agent says "The code compiles cleanly"
//   - Tool returns "0 matches" → agent says "Found 5 occurrences of the pattern"
//   - Tool returns exit code 1 → agent says "Command executed successfully"
//
// This is the NARRATIVE-level manifestation: the agent's textual narrative does
// not match the evidence it just received. Unlike existing detectors:
//   - phantom_verify.go: checks if verification COMMANDS were run (absence of
//     commands). This detector checks if claims match actual command OUTPUTS.
//   - verify_disconnect.go: checks behavioral advancement past failures (ignoring
//     failures). This checks explicit contradiction in text.
//   - claim_verify.go: checks for hidden failure signals in nominally-successful
//     outputs (result-level). This checks the agent's summary vs. result content.
//
// Detection approach:
//   1. Maintains a ring buffer of recent tool outputs (last N results).
//   2. When the agent makes a positive outcome claim in its text, checks whether
//      recent tool outputs contain failure indicators that directly contradict it.
//   3. When the agent makes a numeric claim ("found N items"), checks whether
//      recent tool outputs support that count.
//   4. Fires when a contradiction is detected - the claim category and the
//      contradicting evidence category mismatch.
//
// Design:
//   - Zero LLM cost - pure deterministic pattern matching.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Uses category-matched contradiction: a "tests pass" claim is only flagged
//     when recent test-command output contains failure indicators, not when an
//     unrelated tool failed.
//   - 5-iteration lookback window: contradictions within recent context matter.

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	narrativeEvidenceMaxWarnings = 2
	narrativeEvidenceWindowSize  = 5 // lookback iterations for recent tool outputs
	narrativeEvidenceMaxRecent   = 8 // ring buffer size for recent outputs
)

// narrativeEvidenceResult captures a recent tool result for contradiction checking.
type narrativeEvidenceResult struct {
	toolName  string
	content   string
	iteration int
	isError   bool
}

// narrativeEvidenceState tracks recent tool outputs and detects contradictions.
type narrativeEvidenceState struct {
	warnings   int
	recentOuts []narrativeEvidenceResult // ring buffer (newest appended)
}

func newNarrativeEvidenceState() *narrativeEvidenceState {
	return &narrativeEvidenceState{}
}

func (s *narrativeEvidenceState) reset() {
	s.warnings = 0
	s.recentOuts = nil
}

// recordToolResult stores a tool output in the ring buffer.
func (s *narrativeEvidenceState) recordToolResult(toolName, content string, iteration int, isError bool) {
	if content == "" && !isError {
		return
	}
	// Truncate very long outputs to save memory.
	if len(content) > 4000 {
		content = content[:4000]
	}
	s.recentOuts = append(s.recentOuts, narrativeEvidenceResult{
		toolName:  toolName,
		content:   content,
		iteration: iteration,
		isError:   isError,
	})
	if len(s.recentOuts) > narrativeEvidenceMaxRecent {
		s.recentOuts = s.recentOuts[len(s.recentOuts)-narrativeEvidenceMaxRecent:]
	}
}

// --- Failure indicator patterns per category ---

// narrativeTestFailureRe matches test-failure indicators in tool output.
var narrativeTestFailureRe = regexp.MustCompile(`(?i)(FAIL(ED|URES?)?|test.{0,10}fail|panic:|0 tests? pass(ed)?|0%.*pass|--- FAIL)`)

// narrativeBuildFailureRe matches build/compile failure indicators.
var narrativeBuildFailureRe = regexp.MustCompile(`(?i)(BUILD FAILURE|compilation failed|undefined:|cannot find|syntax error|\.go:\d+:.*error|Error: .*\bnot defined\b|expected .*, found|error TS\d+)`)

// narrativeNoResultRe matches "no results found" / empty output patterns.
var narrativeNoResultRe = regexp.MustCompile(`(?i)(no (results?|matches?|files? found|entries)|0 (results?|matches?|files?)|empty (result|set|list)|nothing (found|to)|no rows)`)

// narrativeNotFoundRe matches "not found" / "does not exist" indicators.
var narrativeNotFoundRe = regexp.MustCompile(`(?i)(not found|does not exist|no such (file|directory|command)|no matching|404)`)

// --- Positive claim patterns in assistant text ---

// narrativeTestPassClaimRe matches claims that tests passed.
var narrativeTestPassClaimRe = regexp.MustCompile(`(?i)(all tests? (pass(ed)?|succeed(ed)?)|tests? (pass(ed)?|are (passing|green|clean))|test suite (passed|succeeds?)|every test (passed|passes))`)

// narrativeBuildPassClaimRe matches claims that build/compilation succeeded.
var narrativeBuildPassClaimRe = regexp.MustCompile(`(?i)(build (pass(es|ed)?|succeed(s|ed)?|is (clean|green|ok))|compiles? (cleanly|successfully|without errors?)|compilation (succeed(s|ed)?|pass(ed|es)?)|no (build|compile|compilation) errors?)`)

// narrativeFoundCountClaimRe matches claims of finding N items/matches/results.
var narrativeFoundCountClaimRe = regexp.MustCompile(`(?i)(found\s+(\d+|a|several|multiple|all)\s+(results?|matches?|occurrences?|items?|entries?|files?|instances?)|(\d+)\s+(results?|matches?|occurrences?|items?)\s+(found|identified|located))`)

// narrativeSuccessClaimRe matches generic success claims for commands.
var narrativeSuccessClaimRe = regexp.MustCompile(`(?i)(command (executed|ran|completed) (successfully|without errors?)|exit(ed)? (with )?code 0|ran (successfully|without (errors?|issues?)))`)

// narrativeAllFoundClaimRe matches claims that all items were found.
var narrativeAllFoundClaimRe = regexp.MustCompile(`(?i)(all\s+(\d+|the)\s+(occurrences?|instances?|matches?|results?|references?)\s+(found|identified|replaced|updated)|found all (references|occurrences|instances|usages))`)

// recentOutputsForCategory finds recent tool outputs matching a category hint.
func (s *narrativeEvidenceState) recentOutputsForCategory(category string, currentIter int) []narrativeEvidenceResult {
	var matched []narrativeEvidenceResult
	for _, ro := range s.recentOuts {
		// Only consider outputs within the lookback window.
		if currentIter-ro.iteration > narrativeEvidenceWindowSize {
			continue
		}
		if !isCategoryRelevant(category, ro.toolName, ro.content) {
			continue
		}
		matched = append(matched, ro)
	}
	return matched
}

// isCategoryRelevant checks if a tool output is relevant to a claim category.
func isCategoryRelevant(category, toolName, content string) bool {
	switch category {
	case "test":
		// Test commands or output containing test indicators.
		return isTestLikeTool(toolName) || narrativeTestFailureRe.MatchString(content)
	case "build":
		// Build commands or output containing build/error indicators.
		return isBuildLikeTool(toolName) || narrativeBuildFailureRe.MatchString(content)
	case "search":
		// Search/grep tools.
		return isSearchLikeTool(toolName)
	case "command":
		// Any run_command/shell execution.
		return isCommandLikeTool(toolName)
	default:
		return true
	}
}

// isTestLikeTool checks if the tool name relates to testing.
func isTestLikeTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "test") || strings.Contains(n, "run") && strings.Contains(n, "command")
}

// isBuildLikeTool checks if the tool name relates to building/compilation.
func isBuildLikeTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "build") || strings.Contains(n, "compile") || strings.Contains(n, "run") && strings.Contains(n, "command")
}

// isSearchLikeTool checks if the tool is a search/grep tool.
func isSearchLikeTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "search") || strings.Contains(n, "grep") || strings.Contains(n, "glob") || strings.Contains(n, "find")
}

// isCommandLikeTool checks if the tool is a shell command executor.
func isCommandLikeTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "command") || strings.Contains(n, "run") || strings.Contains(n, "exec") || strings.Contains(n, "bash")
}

// checkContradiction analyzes assistant text against recent tool outputs
// and returns a guidance message if a narrative-evidence decoupling is found.
func (s *narrativeEvidenceState) checkContradiction(assistantText string, currentIter int) string {
	if s.warnings >= narrativeEvidenceMaxWarnings {
		return ""
	}
	if assistantText == "" || len(s.recentOuts) == 0 {
		return ""
	}

	var contradictions []string

	// Check 1: "tests pass" claim vs. recent test failures.
	if narrativeTestPassClaimRe.MatchString(assistantText) {
		outs := s.recentOutputsForCategory("test", currentIter)
		for _, ro := range outs {
			if narrativeTestFailureRe.MatchString(ro.content) {
				contradictions = append(contradictions, fmt.Sprintf(
					"text claims tests pass, but %s output at iteration %d contains failure indicators",
					ro.toolName, ro.iteration))
				break
			}
		}
	}

	// Check 2: "build compiles" claim vs. recent build failures.
	if len(contradictions) == 0 && narrativeBuildPassClaimRe.MatchString(assistantText) {
		outs := s.recentOutputsForCategory("build", currentIter)
		for _, ro := range outs {
			if narrativeBuildFailureRe.MatchString(ro.content) || ro.isError {
				contradictions = append(contradictions, fmt.Sprintf(
					"text claims build succeeds, but %s output at iteration %d contains compilation errors",
					ro.toolName, ro.iteration))
				break
			}
		}
	}

	// Check 3: "found N items" claim vs. "no results" output from search tools.
	if len(contradictions) == 0 {
		if narrativeFoundCountClaimRe.MatchString(assistantText) || narrativeAllFoundClaimRe.MatchString(assistantText) {
			outs := s.recentOutputsForCategory("search", currentIter)
			for _, ro := range outs {
				if narrativeNoResultRe.MatchString(ro.content) {
					contradictions = append(contradictions, fmt.Sprintf(
						"text claims results were found, but %s output at iteration %d reported no matches",
						ro.toolName, ro.iteration))
					break
				}
			}
		}
	}

	// Check 4: "command succeeded" claim vs. error output from command tools.
	if len(contradictions) == 0 && narrativeSuccessClaimRe.MatchString(assistantText) {
		outs := s.recentOutputsForCategory("command", currentIter)
		for _, ro := range outs {
			if ro.isError {
				contradictions = append(contradictions, fmt.Sprintf(
					"text claims command succeeded, but %s at iteration %d returned an error",
					ro.toolName, ro.iteration))
				break
			}
		}
	}

	if len(contradictions) == 0 {
		return ""
	}

	s.warnings++
	var hint strings.Builder
	hint.WriteString("[narrative-evidence decoupling] Your text narrative contradicts recent tool output evidence:\n")
	for _, c := range contradictions {
		hint.WriteString("  - " + c + "\n")
	}
	hint.WriteString("\nThis is an explanation-decision decoupling failure (arXiv:2605.01604). ")
	hint.WriteString("Re-read the actual tool outputs before making outcome claims. ")
	hint.WriteString("Your narrative must match the evidence - fabricated or hallucinated success ")
	hint.WriteString("claims erode trust in your audit trail and mask real issues.")
	return hint.String()
}

// maybeWarnNarrativeEvidence is the agent-loop entry point.
func (a *Agent) maybeWarnNarrativeEvidence(assistantText string, currentIter int) string {
	if a.narrativeEvidence == nil {
		return ""
	}
	return a.narrativeEvidence.checkContradiction(assistantText, currentIter)
}
