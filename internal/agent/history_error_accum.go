package agent

// History Error Accumulation Detector
//
// Research basis:
//   - HORIZON framework (arXiv:2604.11978, 2026) identifies seven failure-mode
//     categories in long-horizon agents. "History Error Accumulation" is one of
//     the process-level categories (72.5% of failures): agents build on stale
//     or incomplete error resolution, with unaddressed issues from prior tool
//     outputs compounding across iterations until the trajectory collapses.
//   - "Preemptive Detection and Correction of Misaligned Actions" (EMNLP 2025)
//     shows that agents frequently process only a subset of a multi-error
//     output, treating partial resolution as complete. The remaining errors
//     become invisible -- the agent "moves on" without acknowledging them.
//   - "Context Rot" (2026 memory benchmarks) confirms that as context grows,
//     agents increasingly fail to act on earlier error signals still visible
//     in their context window -- a form of cognitive offloading.
//
// Problem: When a tool output (build, test, lint, diagnostics) contains N
// distinct error/failure/warning signals, the agent often addresses only a
// subset in subsequent iterations. The unaddressed issues silently persist
// in the trajectory history but the agent stops referencing them, creating
// a growing gap between known problems and acknowledged problems. This is a
// leading cause of horizon-conditioned performance collapse.
//
// This is NOT the same as:
//   - selective_evidence.go: detects cherry-picking evidence to confirm a
//     hypothesis (confirmation bias) -- the issue there is bias, here it is
//     omission via cognitive overload
//   - verify_disconnect.go: detects advancing past a failure without resolving
//     it -- there the issue is a single failure, here it's a multi-issue
//     output where some are addressed and others dropped
//   - belief_defense.go: detects defending a prior belief against contradicting
//     evidence -- here the agent may not be defending anything, just forgetting
//   - premature_surrender.go: detects giving up on the overall task -- here
//     the agent continues working but has dropped specific sub-problems
//
// Detection approach (zero LLM cost):
//   1. After each tool result, count distinct error/warning indicators
//      (test failures, build errors, lint warnings, diagnostics) in the output.
//   2. Track the assistant text after that tool result for references to
//      those issues (fix language, error references, or explicit dismissal).
//   3. When the gap between "issues found" and "issues acknowledged" exceeds
//      a threshold, inject guidance to re-examine unaddressed items.
//
// Interaction with existing detectors:
//   - Complements verify_disconnect: verify_disconnect catches a single
//     failure being advanced past; this catches multi-issue outputs where
//     some are resolved but others silently dropped
//   - Complements belief_defense: belief_defense catches active suppression;
//     this catches passive omission

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// historyErrAccumMaxWarnings: cap warnings per run.
	historyErrAccumMaxWarnings = 2

	// historyErrAccumMinIssues: minimum issues in a tool output to trigger
	// tracking (a single error is handled by verify_disconnect).
	historyErrAccumMinIssues = 2

	// historyErrAccumGapThreshold: minimum ratio of unacknowledged issues
	// to total issues needed to trigger a warning. E.g., if a tool output
	// had 5 issues and the agent only addresses 1 (4 unacknowledged),
	// the gap ratio is 4/5 = 0.8, well above threshold.
	historyErrAccumGapThreshold = 0.5

	// historyErrAccumResultLimit: max chars of tool result to scan.
	historyErrAccumResultLimit = 8000

	// historyErrAccumTextLimit: max chars of assistant text to scan.
	historyErrAccumTextLimit = 6000
)

// historyErrAccumState tracks warnings and pending issue counts.
type historyErrAccumState struct {
	warnings        int
	pendingIssues   []string // issues from most recent multi-issue tool result
	pendingTool     string   // tool name that produced those issues
	pendingIter     int      // iteration when issues were found
	pendingAcked    int      // how many of pending issues have been acknowledged
	textAccumulator string   // accumulated assistant text after the tool result
}

func newHistoryErrAccumState() *historyErrAccumState {
	return &historyErrAccumState{}
}

func (s *historyErrAccumState) reset() {
	s.warnings = 0
	s.pendingIssues = nil
	s.pendingTool = ""
	s.pendingIter = 0
	s.pendingAcked = 0
	s.textAccumulator = ""
}

// Patterns for detecting distinct error/warning indicators in tool output.
var (
	// Go test failures: "--- FAIL: TestName"
	historyErrTestFailRe = regexp.MustCompile(`(?m)---\s*FAIL:?\s+(\S+)`)

	// Go build/compile errors: "file.go:line:col: error:" or "undefined:"
	historyErrBuildRe = regexp.MustCompile(`(?m)(?:^|\n)[^:\s][^:]*:\d+:\d*:\s+(?:error|undefined|cannot|fatal)`)

	// Generic error lines: "Error:", "ERROR:", "panic:"
	historyErrGenericRe = regexp.MustCompile(`(?m)^\s*(Error|ERROR|panic)\b`)

	// Lint/vet warnings: "warning:", "WARN:", "vet:" -- but only as standalone
	historyErrWarnRe = regexp.MustCompile(`(?m)^\s*(warning|WARNING|WARN)\b`)

	// TypeScript/JS compiler errors: "error TS1234:", "ERROR in", "✖ ERROR"
	historyErrTSRe = regexp.MustCompile(`(?m)(?:error\s+TS\d+|ERROR\s+in|✖\s+ERROR)`)

	// Python traceback / exception lines
	historyErrPyRe = regexp.MustCompile(`(?m)(?:Traceback|Exception|Error:)\s*\n`)
)

// issueAcknowledgmentRe detects language indicating the agent is working on
// or has resolved a specific issue -- referencing fixes, errors, or test names.
var issueAcknowledgmentPatterns = []regexp.Regexp{
	// Fix/resolve language
	*regexp.MustCompile(`(?i)\b(fix(?:ed|ing)?|resolve(?:d)?|address(?:ed|ing)?|handle(?:d)?|correct(?:ed)?|patch(?:ed)?)\b`),
	// Error/issue references
	*regexp.MustCompile(`(?i)\b(error|issue|problem|failure|warning|panic|undefined)\b`),
	// Test/build references
	*regexp.MustCompile(`(?i)\b(test|build|compile|lint|vet|diagnostic)\b`),
}

// countToolOutputIssues counts distinct error/warning indicators in a tool
// result. Returns a deduplicated list of issue summaries.
func countToolOutputIssues(content string) []string {
	scanned := content
	if len(scanned) > historyErrAccumResultLimit {
		scanned = scanned[:historyErrAccumResultLimit]
	}

	issueSet := make(map[string]bool)

	// Test failures
	for _, m := range historyErrTestFailRe.FindAllStringSubmatch(scanned, -1) {
		if len(m) > 1 {
			issueSet["test:"+m[1]] = true
		}
	}

	// Build/compile errors
	buildMatches := historyErrBuildRe.FindAllString(scanned, -1)
	for _, bm := range buildMatches {
		issueSet["build:"+strings.TrimSpace(bm)] = true
	}

	// Generic error lines
	genMatches := historyErrGenericRe.FindAllString(scanned, -1)
	for _, gm := range genMatches {
		issueSet["generic:"+strings.TrimSpace(gm)] = true
	}

	// Warnings (only count if no errors -- warnings alongside errors are
	// secondary signals and the errors already dominate)
	if len(issueSet) == 0 {
		warnMatches := historyErrWarnRe.FindAllString(scanned, -1)
		for _, wm := range warnMatches {
			issueSet["warn:"+strings.TrimSpace(wm)] = true
		}
	}

	// TypeScript/JS errors
	tsMatches := historyErrTSRe.FindAllString(scanned, -1)
	for _, tm := range tsMatches {
		issueSet["ts:"+strings.TrimSpace(tm)] = true
	}

	// Python tracebacks
	pyMatches := historyErrPyRe.FindAllString(scanned, -1)
	for _, pm := range pyMatches {
		issueSet["py:"+strings.TrimSpace(pm)] = true
	}

	result := make([]string, 0, len(issueSet))
	for k := range issueSet {
		result = append(result, k)
	}
	return result
}

// countAcknowledgedIssues estimates how many of the pending issues the
// assistant text references. Uses keyword overlap heuristics.
func countAcknowledgedIssues(text string, issues []string) int {
	if len(text) == 0 || len(issues) == 0 {
		return 0
	}

	scanned := text
	if len(scanned) > historyErrAccumTextLimit {
		scanned = scanned[:historyErrAccumTextLimit]
	}
	scannedLower := strings.ToLower(scanned)

	// Check if text contains any acknowledgment language at all
	hasAckLang := false
	for _, p := range issueAcknowledgmentPatterns {
		if p.MatchString(scanned) {
			hasAckLang = true
			break
		}
	}
	if !hasAckLang {
		return 0
	}

	// Count issues referenced by keyword overlap
	acked := 0
	for _, issue := range issues {
		// Extract the test name or key identifier from the issue string
		parts := strings.SplitN(issue, ":", 2)
		if len(parts) < 2 {
			continue
		}
		identifier := strings.ToLower(parts[1])

		// For test failures, check if the test name appears in text
		if parts[0] == "test" {
			// Use just the test function name
			testName := identifier
			if idx := strings.LastIndex(testName, "/"); idx >= 0 {
				testName = testName[idx+1:]
			}
			if strings.Contains(scannedLower, testName) {
				acked++
			}
			continue
		}

		// For other issue types, check for shared significant keywords
		// Extract words from the identifier and check overlap
		keywords := historyErrExtractKeywords(identifier)
		if len(keywords) == 0 {
			continue
		}
		matched := 0
		for _, kw := range keywords {
			if strings.Contains(scannedLower, kw) {
				matched++
			}
		}
		// If at least half the keywords match, consider it acknowledged
		if matched*2 >= len(keywords) {
			acked++
		}
	}

	return acked
}

// historyErrExtractKeywords pulls out significant words from an issue description.
func historyErrExtractKeywords(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	words := strings.Fields(s)
	var keywords []string
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, ".,;:()[]{}\"'"))
		if len(w) < 3 {
			continue
		}
		// Skip common noise words
		switch w {
		case "the", "and", "for", "with", "from", "line", "file", "this",
			"that", "error", "undefined": // keep "error"/"undefined" out since they're too generic
			continue
		}
		keywords = append(keywords, w)
	}
	return keywords
}

// recordToolResult records a tool result that contains multiple issues.
// Called after each tool execution in the agent loop.
func (s *historyErrAccumState) recordToolResult(toolName string, resultContent string, iter int) {
	issues := countToolOutputIssues(resultContent)
	if len(issues) >= historyErrAccumMinIssues {
		s.pendingIssues = issues
		s.pendingTool = toolName
		s.pendingIter = iter
		s.pendingAcked = 0
		s.textAccumulator = ""
	}
}

// recordAssistantText accumulates text after a multi-issue tool result and
// returns guidance if the acknowledgment gap is significant.
func (s *historyErrAccumState) recordAssistantText(text string, iter int) string {
	if len(s.pendingIssues) < historyErrAccumMinIssues {
		return ""
	}
	if s.warnings >= historyErrAccumMaxWarnings {
		return ""
	}

	s.textAccumulator += "\n" + text
	s.pendingAcked = countAcknowledgedIssues(s.textAccumulator, s.pendingIssues)

	total := len(s.pendingIssues)
	unacked := total - s.pendingAcked
	if unacked <= 0 {
		// All issues acknowledged -- clear tracking
		s.pendingIssues = nil
		return ""
	}

	gapRatio := float64(unacked) / float64(total)
	if gapRatio < historyErrAccumGapThreshold {
		return ""
	}

	// Only warn after at least one iteration has passed since the issues
	// were found (give the agent a chance to address them first).
	if iter <= s.pendingIter {
		return ""
	}

	s.warnings++

	// Build list of unacknowledged issues
	var unackedIssues []string
	ackedSet := make(map[string]bool)
	for _, issue := range s.pendingIssues {
		// Re-check each issue against accumulated text
		parts := strings.SplitN(issue, ":", 2)
		if len(parts) < 2 {
			continue
		}
		identifier := strings.ToLower(parts[1])
		textLower := strings.ToLower(s.textAccumulator)
		matched := false
		if parts[0] == "test" {
			testName := identifier
			if idx := strings.LastIndex(testName, "/"); idx >= 0 {
				testName = testName[idx+1:]
			}
			if strings.Contains(textLower, testName) {
				matched = true
			}
		} else {
			kw := historyErrExtractKeywords(identifier)
			if len(kw) > 0 {
				hits := 0
				for _, k := range kw {
					if strings.Contains(textLower, k) {
						hits++
					}
				}
				if hits*2 >= len(kw) {
					matched = true
				}
			}
		}
		if !matched {
			ackedSet[issue] = false
			display := parts[1]
			if len(display) > 80 {
				display = display[:77] + "..."
			}
			unackedIssues = append(unackedIssues, "  - "+display)
		} else {
			ackedSet[issue] = true
		}
	}

	if len(unackedIssues) == 0 {
		return ""
	}

	// Cap displayed issues
	if len(unackedIssues) > 6 {
		unackedIssues = unackedIssues[:6]
		unackedIssues = append(unackedIssues, "  - ... and possibly more")
	}

	return fmt.Sprintf(
		"[history-error-accumulation] Tool output from %s (iteration %d) contained %d distinct "+
			"issue(s), but your subsequent actions only appear to address %d of them. "+
			"%d issue(s) remain unacknowledged in the trajectory history. "+
			"This is a known failure mode in long-horizon agents -- unaddressed issues "+
			"compound silently and cause trajectory collapse later (HORIZON framework, "+
			"arXiv:2604.11978). "+
			"Review the following unaddressed items from that earlier output and either "+
			"resolve them or explicitly determine they are irrelevant:\n"+
			"%s",
		s.pendingTool, s.pendingIter, total, total-unacked, unacked,
		strings.Join(unackedIssues, "\n"),
	)
}
