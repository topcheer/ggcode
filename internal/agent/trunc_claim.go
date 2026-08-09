package agent

// Truncated Output Completeness Fallacy Detector
//
// Research basis:
//   - AgentMarketCap 2026 production report: 12-18% of agent tool calls
//     fail in production. A key failure mode is agents treating truncated
//     tool outputs as exhaustive. When grep/read_file/search returns partial
//     results (truncated by guardToolOutput), the agent proceeds to make
//     completeness claims -- "only 10 files match", "no references found",
//     "these are all the occurrences" -- based on data that was cut off.
//   - Microsoft AI Red Team 2026: "False Exhaustiveness" -- agents declare
//     they've found all instances of a pattern when the search results
//     were actually truncated mid-stream. This is a silent failure: the
//     agent doesn't know it missed data, and the user doesn't either.
//   - OpenLayer 2026 agent failure taxonomy: "Truncated-Result Conclusions"
//     rank among the top causes of incorrect code changes. The agent
//     modifies code based on an incomplete picture of the codebase.
//   - IBM STRATUS (NeurIPS 2025): Information sufficiency checks -- agents
//     must verify they have complete data before making exhaustive claims.
//     Truncated results violate this precondition silently.
//
// Problem: guardToolOutput truncates large tool results when context is
// under pressure. The truncation_advisory.go tells the agent HOW to retrieve
// more data. But NO detector checks whether the agent ACTUALLY acknowledged
// the truncation or instead proceeded to make completeness claims as if
// it had the full dataset.
//
// This detector:
//   1. Records truncation events (tool name + iteration when output was cut)
//   2. On the next iteration, scans assistant text for exhaustiveness claims
//   3. If completeness claims are found after a truncation event, injects
//      guidance to verify completeness before drawing conclusions
//
// Existing detectors that are RELATED but do NOT cover this:
//   - truncation_advisory.go: provides retrieval guidance (HOW to get more),
//     not a behavioral check on whether the agent ignored it.
//   - evidence_overconfidence.go: tracks tool-TYPE calibration asymmetry
//     (evidence tools vs verification tools), not truncation awareness.
//   - tool_output_guard.go: performs the truncation, doesn't verify the
//     agent's response to it.
//   - search_result_invalidation.go: detects stale results after edits,
//     not truncated results before claims.
//
// Design:
//   - Zero LLM cost -- pure deterministic text pattern matching
//   - Tracks at most 8 truncation events (memory bound)
//   - Fires at most 2 times per run (advisory, non-blocking)
//   - Checks the iteration immediately after truncation (temporal locality)
//   - Resets each user turn

import (
	"regexp"
	"strings"
)

const (
	truncClaimMaxWarnings  = 2
	truncClaimMaxEvents    = 8
	truncClaimExcerptLimit = 120
)

// truncationEvent records when a tool output was truncated.
type truncationEvent struct {
	toolName  string
	iteration int
}

// truncClaimState tracks truncation events and detects when the agent
// makes completeness claims after receiving truncated results.
type truncClaimState struct {
	events       []truncationEvent
	warnings     int
	lastWarnIter int
}

func newTruncClaimState() *truncClaimState {
	return &truncClaimState{lastWarnIter: -1}
}

func (s *truncClaimState) reset() {
	s.events = nil
	s.warnings = 0
	s.lastWarnIter = -1
}

// recordTruncation registers that a tool result was truncated.
// Called when guardToolOutput reduces a tool result's size.
func (s *truncClaimState) recordTruncation(toolName string, iteration int) {
	if len(s.events) >= truncClaimMaxEvents {
		s.events = s.events[1:] // ring buffer: drop oldest
	}
	s.events = append(s.events, truncationEvent{
		toolName:  toolName,
		iteration: iteration,
	})
}

// hasRecentTruncation checks if any truncation occurred at or before
// the given iteration, with temporal locality of 1 iteration window.
func (s *truncClaimState) hasRecentTruncation(iteration int) (truncationEvent, bool) {
	for i := len(s.events) - 1; i >= 0; i-- {
		ev := s.events[i]
		// Check if truncation happened in the same iteration (tool results
		// are processed before assistant text in the same loop pass) or
		// the previous iteration.
		if ev.iteration == iteration || ev.iteration == iteration-1 {
			return ev, true
		}
	}
	return truncationEvent{}, false
}

// completenessClaimPatterns detects language asserting exhaustive or
// complete findings. Case-insensitive. These represent the agent treating
// partial (truncated) data as the full picture.
var completenessClaimPatterns = []*regexp.Regexp{
	// Exhaustive count claims
	regexp.MustCompile(`(?i)\b(?:only|exactly|just|precisely)\s+\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\s+(?:in total|total|altogether|found)\b`),
	regexp.MustCompile(`(?i)\btotal\s+of\s+\d+\s+(?:file|match|result|occurrence|reference|instance)(?:es|s)?\b`),

	// Exhaustiveness assertions
	regexp.MustCompile(`(?i)\b(?:these|the|those)\s+(?:are\s+)?(?:all|the only|every)\s+(?:the\s+)?(?:file|match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:that'?s|this is|it is|those are|these are)\s+(?:all|everything)(?:\s+we)?\s+(?:found|see|have)?\b`),
	regexp.MustCompile(`(?i)\b(?:all|every|each)\s+(?:occurrence|instance|reference|match|location)(?:es|s)?\s+(?:of|to)\b`),

	// Negative exhaustiveness (claiming absence based on truncated search)
	regexp.MustCompile(`(?i)\bno\s+(?:other|more|further|additional)\s+(?:file|match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:nothing|no)\s+else\s+(?:match|found|exist|appear)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\bthere\s+(?:are|is)\s+no\s+(?:other|more|further)\s+(?:file|match|result|reference|occurrence|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:couldn'?t|can'?t|did not|didn'?t)\s+find\s+(?:any\s+)?(?:other|more|further)\s+(?:file|match|result|reference|occurrence|instance)(?:es|s)?\b`),

	// "Found all" / "all results" claims
	regexp.MustCompile(`(?i)\bfound\s+(?:all|every)\s+(?:the\s+)?(?:match|result|occurrence|reference|instance|location)(?:es|s)?\b`),
	regexp.MustCompile(`(?i)\b(?:search|grep|scan)\s+(?:found|returned|revealed)\s+(?:all|every)\b`),

	// Complete enumeration
	regexp.MustCompile(`(?i)\b(?:here'?s|here is)\s+(?:the\s+)?(?:complete|full|comprehensive|exhaustive)\s+(?:list|set|overview|summary)s?\b`),
}

// truncationAcknowledged checks if the assistant text acknowledges the
// truncation (meaning the agent is aware the data may be incomplete).
func truncationAcknowledged(text string) bool {
	lower := strings.ToLower(text)
	ackPhrases := []string{
		"truncat",
		"output was cut",
		"results may be incomplete",
		"may be missing",
		"not exhaustive",
		"partial results",
		"some results may be missing",
		"need to check if there are more",
		"there might be more",
		"could be more results",
	}
	for _, p := range ackPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// maybeWarnTruncClaim checks assistant text for completeness claims made
// after a truncation event. Returns guidance string if detected, "" otherwise.
func (s *truncClaimState) maybeWarnTruncClaim(text string, iteration int) string {
	if s.warnings >= truncClaimMaxWarnings {
		return ""
	}
	if len(text) == 0 {
		return ""
	}

	// Don't warn on the same iteration twice
	if s.lastWarnIter == iteration {
		return ""
	}

	ev, ok := s.hasRecentTruncation(iteration)
	if !ok {
		return ""
	}

	// If the agent acknowledged truncation, it's handling it well
	if truncationAcknowledged(text) {
		return ""
	}

	// Scan for completeness claims
	claims := 0
	var firstMatch string
	for _, p := range completenessClaimPatterns {
		loc := p.FindStringIndex(text)
		if loc != nil {
			claims++
			if firstMatch == "" {
				end := loc[1]
				if end-loc[0] > truncClaimExcerptLimit {
					end = loc[0] + truncClaimExcerptLimit
				}
				firstMatch = text[loc[0]:end]
			}
		}
		if claims >= 2 {
			break
		}
	}

	if claims == 0 {
		return ""
	}

	s.warnings++
	s.lastWarnIter = iteration

	toolLabel := ev.toolName
	if toolLabel == "" {
		toolLabel = "a previous tool"
	}

	return "[Truncated Output Completeness Check] The output from " + toolLabel +
		" was truncated in a recent call, but your response makes a completeness/exhaustiveness claim (\"" +
		strings.TrimSpace(firstMatch) + "...\". " +
		"The truncation means some results may have been cut off. " +
		"Before asserting exhaustive findings: (1) re-run the search with narrower scope " +
		"or filters to get complete results, (2) use offset/pagination to check for additional matches, " +
		"(3) explicitly acknowledge if results may be incomplete. " +
		"Do not claim \"all\", \"only N\", or \"no other\" unless you've verified the full dataset."
}
