package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Premature Surrender Detector -- Metacognitive Task Abandonment Awareness
//
// Research basis:
//   - "Truly Self-Improving Agents Require Intrinsic Metacognitive Learning"
//     (arXiv:2506.05109): agents need intrinsic metacognitive awareness to
//     recognize when they're abandoning tasks prematurely rather than trying
//     alternative strategies. Without this, agents settle into local optima.
//   - "Metagent-P: A Neuro-Symbolic Planning Agent with Metacognition for
//     Open-Endedness" (ACL 2025 Findings): metacognitive monitoring of
//     strategy selection prevents premature convergence on suboptimal paths.
//   - "How Do LLMs Fail In Agentic Scenarios?" (arXiv:2512.07497):
//     "premature surrender" -- agents giving up after minimal failures -- is
//     a distinct failure mode from errors or tool misuse.
//
// The gap: ggcode has detectors for tool errors, oscillation, stagnation,
// and last-mile stalls, but NONE detect the moment the agent decides to give
// up on a task or sub-goal prematurely. Agents often signal surrender in their
// text ("this isn't possible", "I can't do X", "let's skip this") when they
// hit a wall, rather than trying alternative approaches. This detector catches
// that surrender language early and pushes the agent to try alternatives.
//
// Key distinction from existing detectors:
//   - Strategy Stagnation: detects retrying the SAME failed approach
//   - Assumption Tracker: detects unverified assumptions in reasoning
//   - Futile Cycle: detects circular exploration without writes
//   - This detector: detects SURRENDER LANGUAGE indicating the agent is
//     abandoning a goal rather than trying a different strategy
//
// The detector:
//  1. Scans assistant text for surrender/give-up language patterns
//  2. Also tracks recent tool failures (error count in recent iterations)
//  3. When surrender language appears AND the agent has budget remaining
//     (not in the last 2 iterations), injects guidance to try alternatives

const (
	maxSurrenderWarnings = 1 // fire at most once per run
	surrenderMinIterLeft = 2 // need at least 2 iterations remaining to push back
)

// surrenderPhrases are patterns that indicate the agent is giving up.
// These are checked case-insensitively.
var surrenderPhrases = []regexp.Regexp{
	// Direct refusal / inability
	*regexp.MustCompile(`(?i)I can'?t (do|complete|implement|achieve|figure out|fix|handle) this`),
	*regexp.MustCompile(`(?i)this (is|seems) (impossible|infeasible|not feasible|not possible)`),
	*regexp.MustCompile(`(?i)I'?m unable to (complete|implement|fix|achieve|resolve)`),
	*regexp.MustCompile(`(?i)there'?s no way to`),
	*regexp.MustCompile(`(?i)cannot be (done|achieved|implemented|resolved)`),
	*regexp.MustCompile(`(?i)it (would be|is) better to (skip|abandon|give up|move on from)`),
	*regexp.MustCompile(`(?i)let'?s (skip|abandon|give up on|move on from) this`),
	*regexp.MustCompile(`(?i)this (approach|method|strategy) (won'?t|will never|cannot) work`),
	*regexp.MustCompile(`(?i)I (don'?t|do not) think (this|we) can`),
	*regexp.MustCompile(`(?i)beyond (my|the) (capabilities|ability|scope)`),
	// Defer / partial completion language when task was requested
	*regexp.MustCompile(`(?i)this (would|will) require (a lot of|significant|extensive) (work|effort|changes)`),
	*regexp.MustCompile(`(?i)let'?s (leave|skip) (this|that) for now`),
	*regexp.MustCompile(`(?i)out of scope for this (task|change|session|pr)`),
}

// surrenderState tracks surrender language detection across a run.
type surrenderState struct {
	fired      bool
	errorCount int // recent tool errors (proxies for hitting a wall)
}

func newSurrenderState() *surrenderState {
	return &surrenderState{}
}

func (s *surrenderState) reset() {
	s.fired = false
	s.errorCount = 0
}

// recordToolError tracks tool failures as context for surrender detection.
func (s *surrenderState) recordToolError() {
	s.errorCount++
}

// checkSurrender scans assistant text for premature surrender language.
// Returns a non-empty guidance message if the pattern is detected.
//
// Parameters:
//   - assistantText: the LLM's text response for this iteration
//   - currentIter: 1-based current iteration number
//   - maxIter: maximum iteration budget
func (s *surrenderState) checkSurrender(assistantText string, currentIter, maxIter int) string {
	if s.fired {
		return ""
	}
	if strings.TrimSpace(assistantText) == "" {
		return ""
	}

	// Only fire if we have meaningful budget remaining -- if we're at the
	// last 1-2 iterations, the agent may legitimately be wrapping up.
	itersRemaining := maxIter - currentIter
	if itersRemaining < surrenderMinIterLeft {
		return ""
	}

	// Scan for surrender phrases
	matched := false
	for _, re := range surrenderPhrases {
		if re.MatchString(assistantText) {
			matched = true
			break
		}
	}
	if !matched {
		return ""
	}

	s.fired = true
	debug.Log("surrender-detect", "premature surrender language detected at iter %d/%d (recent_errors=%d)", currentIter, maxIter, s.errorCount)

	return fmt.Sprintf(
		"[premature-surrender] Abandoning task with %d iterations remaining. Try a different approach or state what's blocking you.",
		itersRemaining,
	)
}

// hasSurrenderPhrase is a test helper that checks text for surrender patterns.
func hasSurrenderPhrase(text string) bool {
	for _, re := range surrenderPhrases {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
