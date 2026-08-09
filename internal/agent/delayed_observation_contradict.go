package agent

// Delayed Observation Contradiction Detector
//
// Research basis:
//   - DRIFT: Detecting Representational Inconsistencies for Factual Truthfulness
//     (2026): lightweight probes catch factually wrong generations. This detector
//     applies the same principle to agent trajectories -- the agent's later text
//     drifts from ("represents inconsistently") its own earlier observations.
//   - Task2Quiz (2026, "What Do LLM Agents Know About Their World?"): decouples
//     task execution from environment understanding and shows that agents
//     frequently fail to maintain an accurate model of facts they have already
//     observed, especially after intervening actions.
//   - Agentic Uncertainty Reveals Agentic Overconfidence (2026): agents are
//     systematically overconfident about facts no longer in their immediate
//     attention window.
//   - LUMINA (2026, arXiv long-horizon): measures capability criticality across
//     long horizons; state-tracking failures compound with distance.
//
// Problem: AI coding agents observe a NEGATIVE outcome from a *successful* tool
// call (exit code 0, isError=false) -- e.g. grep returns "No matches found",
// glob returns an empty list, a search returns "no results", a build returns a
// FAIL marker captured in stdout rather than via a non-zero exit, or a read
// returns "file not found" as content. Several turns later, after intervening
// tool calls, the agent re-asserts the POSITIVE opposite ("the pattern exists",
// "found N matches", "the file contains", "build passes") -- it has lost track
// of its own earlier observation. This is representational inconsistency over
// distance.
//
// Why existing detectors miss it:
//   - false_premise_check.go: only fires on isError==true tool errors, and uses
//     a 2-turn freshness window. A delayed contradiction (>=3 turns later) and a
//     *successful* negative result both slip through.
//   - narrative_evidence.go: scans a small recent ring buffer; once the negative
//     observation rotates out, a later contradicting claim is undetected.
//   - contradiction.go: catches root-cause *reversals* between turns, not
//     denial of an observed environment fact.
//   - verify_disconnect.go: behavioral advancement past failures, not textual
//     denial of a specific observed fact.
//
// This detector fills the gap: it persists negative observations from successful
// tool calls (no freshness eviction), and flags when a later positive claim
// directly contradicts one that has aged beyond all other detectors' windows
// (the "delayed" threshold), filling the long-distance blind spot.
//
// Design:
//   - Zero LLM cost -- pure deterministic pattern matching.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Only flags contradictions with delay >= delayedContradictionMinTurns
//     (default 3), so it complements rather than duplicates the adjacent-turn
//     detectors.
//   - Observations are content-categorized (no-match, not-found, empty-result,
//     build-fail-content) and matched against category-specific positive claims.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	delayedContradictionMaxWarnings = 2
	delayedContradictionMinTurns    = 3 // turns since observation before flagging (complements 2-turn freshness windows)
	delayedContradictionMaxObs      = 16
	delayedContradictionMaxExamples = 3
)

// docObsCategory is the content category of a negative observation.
type docObsCategory int

const (
	docObsNone      docObsCategory = iota
	docObsNoMatch                  // grep/search "no matches"
	docObsNotFound                 // read/list "not found" / "no such file"
	docObsEmpty                    // glob/search/list returned empty
	docObsBuildFail                // build/test output contains explicit failure marker but exit 0
)

func (c docObsCategory) String() string {
	switch c {
	case docObsNoMatch:
		return "no-match"
	case docObsNotFound:
		return "not-found"
	case docObsEmpty:
		return "empty-result"
	case docObsBuildFail:
		return "build-failure"
	default:
		return "unknown"
	}
}

// docObservation records a single negative observation from a successful tool call.
type docObservation struct {
	category docObsCategory
	toolName string
	snippet  string // short evidence excerpt
	turn     int    // turn index when observed
	matched  bool   // set true once a delayed contradiction is attributed
}

// delayedObservationState persists negative observations and tracks warnings.
type delayedObservationState struct {
	observations []docObservation
	currentTurn  int
	warningCount int
}

func newDelayedObservationState() *delayedObservationState {
	return &delayedObservationState{}
}

func (s *delayedObservationState) reset() {
	s.observations = nil
	s.currentTurn = 0
	s.warningCount = 0
}

// advanceTurn bumps the turn counter at the start of each assistant turn.
func (s *delayedObservationState) advanceTurn() {
	s.currentTurn++
}

// recordToolResult captures negative observations from *successful* tool calls.
// isError results are ignored -- false_premise_check already handles those.
func (s *delayedObservationState) recordToolResult(toolName, resultContent string, isError bool) {
	if isError {
		return
	}
	cat := docCategorizeNegative(toolName, resultContent)
	if cat == docObsNone {
		return
	}
	snippet := strings.TrimSpace(resultContent)
	if len(snippet) > 120 {
		snippet = snippet[:117] + "..."
	}
	s.observations = append(s.observations, docObservation{
		category: cat,
		toolName: toolName,
		snippet:  snippet,
		turn:     s.currentTurn,
	})
	// Keep bounded; drop oldest.
	if len(s.observations) > delayedContradictionMaxObs {
		s.observations = s.observations[len(s.observations)-delayedContradictionMaxObs:]
	}
}

// docCategorizeNegative determines whether a successful tool result contains a
// negative/empty outcome worth tracking. Returns docObsNone if the result is a
// genuine positive or non-categorized.
func docCategorizeNegative(toolName, content string) docObsCategory {
	low := strings.ToLower(content)

	if docIsNoMatchContent(low) {
		return docObsNoMatch
	}
	if docIsNotFoundContent(low) {
		return docObsNotFound
	}
	if docIsEmptyResult(toolName, content, low) {
		return docObsEmpty
	}
	if docIsBuildFailContent(toolName, low) {
		return docObsBuildFail
	}
	return docObsNone
}

// --- Negative-content indicators (applied to successful results) ---

func docIsNoMatchContent(low string) bool {
	for _, ind := range []string{
		"no matches", "no results found", "0 matches", "zero matches",
		"did not match", "no matching", "returned 0", "no occurrences",
		"0 results", "no hits",
	} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

func docIsNotFoundContent(low string) bool {
	for _, ind := range []string{
		"no such file", "file not found", "not found",
		"does not exist", "cannot find", "no files found",
	} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

// docIsEmptyResult detects tools that returned structurally empty output.
func docIsEmptyResult(toolName, content, low string) bool {
	switch toolName {
	case "glob", "list_directory", "lsp_references", "lsp_workspace_symbols",
		"lsp_implementation", "search_files", "grep":
	default:
		return false
	}
	// Structural tools: empty/whitespace-only content is a meaningful empty result.
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	// Some tools print "(0 files)" or "0 entries" style summaries.
	for _, ind := range []string{"0 files", "0 entries", "0 symbols", "0 items"} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

// docIsBuildFailContent detects an explicit build/test failure marker in the
// stdout of a *successful* (exit 0) command capture. This catches the case
// where a command wrapper swallows the exit code but the content still shows FAIL.
func docIsBuildFailContent(toolName, low string) bool {
	switch toolName {
	case "run_command", "start_command", "read_command_output", "wait_command":
	default:
		return false
	}
	if !strings.Contains(low, "fail") && !strings.Contains(low, "error") {
		return false
	}
	// Require a specific failure marker to avoid false positives on e.g. error-handling code text.
	for _, ind := range []string{
		"build failed", "tests failed", "test failed", "compilation failed",
		"exit code 1", "exit: 1", "failed (", "failed:", "error:",
	} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

// --- Positive-claim matchers (the agent's contradicting assertion) ---

var docFoundCountRe = regexp.MustCompile(`(?i)(found\s+[0-9]+\s+(match|result|file|reference|occurrence|symbol|implementation)|returned\s+[0-9]+\s+(match|result))`)

var docExistsRe = regexp.MustCompile(`(?i)(the\s+file\s+(exists|contains|has)|file\s+content\s+(is|shows|contains)|i\s+read\s+the\s+file|contents?\s+of\s+(the|this)\s+file|the\s+pattern\s+(exists|is\s+(present|found|there)))`)

var docBuildPassRe = regexp.MustCompile(`(?i)(build\s+(pass|succeed|is\s+green|is\s+ok)|tests?\s+(pass|all\s+pass|succeed)|compiles?\s+(successfully|clean)|the\s+(build|tests?)\s+(pass|passed|succeed))`)

// docMatchingPositiveClaim returns the matched positive-claim text for the given
// category, or "" if no contradicting positive claim is present.
func docMatchingPositiveClaim(category docObsCategory, text string) string {
	switch category {
	case docObsNoMatch, docObsEmpty:
		if m := docFoundCountRe.FindString(text); m != "" {
			return m
		}
	case docObsNotFound:
		if m := docExistsRe.FindString(text); m != "" {
			return m
		}
	case docObsBuildFail:
		if m := docBuildPassRe.FindString(text); m != "" {
			return m
		}
	}
	return ""
}

// checkDelayedContradiction scans assistant text for positive claims that
// contradict an aged negative observation. Returns a guidance hint if found.
func (s *delayedObservationState) checkDelayedContradiction(assistantText string) string {
	if s == nil || len(s.observations) == 0 {
		return ""
	}
	if s.warningCount >= delayedContradictionMaxWarnings {
		return ""
	}

	var hits []docObservation
	for i := range s.observations {
		obs := &s.observations[i]
		if obs.matched {
			continue
		}
		delay := s.currentTurn - obs.turn
		if delay < delayedContradictionMinTurns {
			continue // too fresh; adjacent-turn detectors handle it
		}
		if claim := docMatchingPositiveClaim(obs.category, assistantText); claim != "" {
			obs.matched = true
			hits = append(hits, docObservation{
				category: obs.category,
				toolName: obs.toolName,
				snippet:  obs.snippet + " | contradicted by: \"" + claim + "\"",
				turn:     obs.turn,
			})
		}
	}

	if len(hits) == 0 {
		return ""
	}

	s.warningCount++

	var examples []string
	for i, h := range hits {
		if i >= delayedContradictionMaxExamples {
			break
		}
		ex := h.snippet
		if len(ex) > 100 {
			ex = ex[:97] + "..."
		}
		examples = append(examples, fmt.Sprintf("  - [%s] turn %d via %s: %s", h.category, h.turn, h.toolName, ex))
	}

	hint := fmt.Sprintf(`[Delayed Observation Contradiction] A positive claim in your text contradicts an earlier observation that is now %d+ turns old:

%s

Your later assertion drifted from a fact you previously observed (DRIFT, 2026; Task2Quiz). Re-verify the current state with a fresh tool call before re-asserting a positive outcome you previously found to be negative. Representational inconsistency over distance is a leading cause of silent agent failure.`,
		delayedContradictionMinTurns, strings.Join(examples, "\n"))

	debug.Log("delayed-obs-contradict", "detected %d delayed contradictions", len(hits))
	return hint
}
