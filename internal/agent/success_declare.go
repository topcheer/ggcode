package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Premature Success Declaration Detector
//
// Research basis: Metacognitive calibration research (Nelson & Narens, 1990;
// MetaCogAgent, arXiv:2605.17292) shows that agents exhibit calibration gaps
// between their stated confidence and actual task state. A specific manifestation
// is "premature success declaration": the agent explicitly claims the task is
//
// This is distinct from existing detectors:
//   - unverifiedConfidence: checks overconfident language without verification
//     (lexical pattern at time of utterance)
//   - planAbandon: checks if a stated plan was not executed
//   - bareEditStreak: checks consecutive edits without verification
//
// This detector tracks CALIBRATION over time: it records when the agent declared
// success, then checks whether subsequent iterations contain tool calls that
// contradict that declaration. If the agent does meaningful work AFTER claiming
// completion, it's a metacognitive self-monitoring failure.
//
// Design:
//   - Records the iteration and text of each success declaration
//   - If tool calls occur in N+2 or later iterations (allowing one verification
//     round after the claim), the calibration gap is flagged
//   - Non-blocking: advisory guidance injected into context
//   - Zero LLM cost - pure heuristic
//   - Fires at most once per run to avoid noise
//   - Resets each run

const (
	// sdMinActionsAfterDecl is the minimum number of tool calls after a success
	// declaration to trigger the detector. A single follow-up action might be
	// cleanup; 2+ suggests the task wasn't actually complete.
	sdMinActionsAfterDecl = 2

	// sdMaxWarns caps warnings per run.
	sdMaxWarns = 1
)

// sdDeclPhrases are explicit success/completion declarations.
var sdDeclPhrases = []string{
	"task is complete",
	"task is done",
	"all done",
	"all set",
	"we're done",
	"i'm done",
	"i am done",
	"work is complete",
	"implementation is complete",
	"implementation is finished",
	"the fix is ready",
	"changes are complete",
	"changes are ready",
	"everything is working",
	"everything works now",
	"the issue is resolved",
	"issue is fixed",
	"problem is solved",
	"successfully implemented",
	"successfully completed",
	"finished implementing",
}

// sdCaveatPhrases indicate the declaration is hedged/conditional, not a
// definitive completion claim. We skip these to avoid false positives.
var sdCaveatPhrases = []string{
	"once you",
	"after you",
	"if you",
	"when you",
	"before we can consider this done",
	"still need to",
	"next step",
	"remaining",
	"however",
	"but first",
	"caveat",
	"note that",
}

// successDeclareState tracks premature success declarations.
type successDeclareState struct {
	mu              sync.Mutex
	declarationIter int    // iteration index where success was declared (-1 = none)
	declarationTxt  string // snippet of the declaration text
	actionsSince    int    // tool calls since the declaration
	warnCount       int    // warnings issued this run
	fired           bool   // whether the detector has fired this run
}

func newSuccessDeclareState() *successDeclareState {
	return &successDeclareState{declarationIter: -1}
}

func (s *successDeclareState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.declarationIter = -1
	s.declarationTxt = ""
	s.actionsSince = 0
	s.warnCount = 0
	s.fired = false
}

// recordAssistantText checks if the assistant text contains a success declaration.
// If so, it records the declaration point (only if not already tracking one).
func (s *successDeclareState) recordAssistantText(text string, iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Only track the first declaration per run.
	if s.declarationIter >= 0 {
		return
	}

	lower := strings.ToLower(text)
	if !sdContainsDeclaration(lower) {
		return
	}

	// Don't count if the declaration is heavily hedged with caveats.
	if sdHasCaveat(lower) {
		return
	}

	s.declarationIter = iter
	// Store a short snippet for the debug log.
	snippet := text
	if len(snippet) > 120 {
		snippet = snippet[:120] + "..."
	}
	s.declarationTxt = snippet
	s.actionsSince = 0
	debug.Log("agent", "Success declaration recorded at iteration %d: %s", iter, snippet)
}

// recordToolCall increments the action counter if we're tracking a declaration.
func (s *successDeclareState) recordToolCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.declarationIter >= 0 {
		s.actionsSince++
	}
}

// maybeWarn checks if enough actions have occurred after a success declaration
// to indicate a premature claim. Returns guidance text if so.
func (s *successDeclareState) maybeWarn(currentIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired || s.warnCount >= sdMaxWarns {
		return ""
	}
	if s.declarationIter < 0 {
		return ""
	}

	// Need at least sdMinActionsAfterDecl tool calls after the declaration,
	// and they must be in a later iteration (not the same one, which might
	// be cleanup/verification of the declared result).
	if s.actionsSince < sdMinActionsAfterDecl {
		return ""
	}
	if currentIter <= s.declarationIter+1 {
		return ""
	}

	s.fired = true
	s.warnCount++

	return `[Metacognitive Calibration] You declared task completion at iteration ` +
		itoaSD(s.declarationIter+1) + `, but have since taken ` + itoaSD(s.actionsSince) +
		` additional tool call actions. This indicates your success declaration was premature: ` +
		`the task was not actually complete when you said it was. ` +
		`Before declaring completion, verify: (1) the build passes, (2) tests pass, ` +
		`(3) all requirements from the original request are met. ` +
		`Avoid claiming "done" while unresolved work remains. ` +
		`If additional work was discovered after your declaration, acknowledge the gap explicitly.`
}

// sdContainsDeclaration checks if text contains any explicit success phrase.
func sdContainsDeclaration(lowerText string) bool {
	for _, phrase := range sdDeclPhrases {
		if strings.Contains(lowerText, phrase) {
			return true
		}
	}
	return false
}

// sdHasCaveat checks if text contains hedging that makes the declaration conditional.
func sdHasCaveat(lowerText string) bool {
	for _, caveat := range sdCaveatPhrases {
		if strings.Contains(lowerText, caveat) {
			return true
		}
	}
	return false
}

// itoaSD is a local int-to-string to avoid importing strconv just for this.
func itoaSD(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
