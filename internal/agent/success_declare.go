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

// sdDeclPhrases are explicit success/completion declarations. Word-boundary
// safe: "all set" must not match "ball setup" / "wall setup" (#352).
var sdDeclPhrases = []string{
	"task is complete",
	"task complete",
	"task is done",
	"all done",
	"all set",
	"we're done",
	"i'm done",
	"i am done",
	"done",
	"work is complete",
	"implementation is complete",
	"implementation is finished",
	"the fix is ready",
	"the fix works",
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
// definitive completion claim. IMPORTANT (#352): these are matched ONLY
// within a small window around the declaration phrase — standard wrap-up
// language elsewhere in the reply ("note that I also refactored X",
// "however, see the caveats below") must not veto the claim. “remaining”
// and “note that”/“however”/“next step” were previously matched against
// the FULL text, discarding 50-80% of legitimate wrap-up declarations
// ("All done. There are no remaining issues." self-vetoed).
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

// sdCaveatWindow is how many characters before/after the declaration
// phrase to search for hedging caveats (mirrors premature_success.go's
// 40-char window precedent, slightly widened).
const sdCaveatWindow = 50

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
	declPhrase, declIdx := sdFindDeclaration(lower)
	if declIdx < 0 {
		return
	}

	// Caveat check is WINDOWED to the declaration phrase (#352): hedging
	// right around the claim vetoes it; wrap-up language elsewhere in the
	// reply does not. Full-text matching previously discarded 50-80% of
	// legitimate declarations and permanently blinded this detector (only
	// the first declaration is ever tracked).
	if sdHasCaveatWindowed(lower, declIdx, declIdx+len(declPhrase)) {
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

// sdFindDeclaration returns the first declaration phrase found and its
// byte index in the (lowercased) text, or ("", -1) if none. "done" gets a
// word-boundary check so "download"/"done_right" style matches are avoided.
func sdFindDeclaration(lowerText string) (string, int) {
	for _, phrase := range sdDeclPhrases {
		// Short phrases that commonly appear as substrings of ordinary words
		// ("all set" in "wall setup", bare "done" in "download") need word
		// boundaries (#352).
		if phrase == "done" || phrase == "all set" {
			var idx int
			if phrase == "done" {
				idx = sdIndexDone(lowerText)
			} else {
				idx = sdIndexWord(lowerText, phrase)
			}
			if idx >= 0 {
				return phrase, idx
			}
			continue
		}
		if idx := strings.Index(lowerText, phrase); idx >= 0 {
			return phrase, idx
		}
	}
	return "", -1
}

// sdIndexWord finds word as a standalone token (word boundaries on both
// sides); returns its index or -1.
func sdIndexWord(lowerText, word string) int {
	for from := 0; ; {
		idx := strings.Index(lowerText[from:], word)
		if idx < 0 {
			return -1
		}
		i := from + idx
		beforeOK := i == 0 || !isWordByte(lowerText[i-1])
		afterOK := i+len(word) >= len(lowerText) || !isWordByte(lowerText[i+len(word)])
		if beforeOK && afterOK {
			return i
		}
		from = i + len(word)
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// sdIndexDone finds "done" as a standalone word, skipping code-identifier
// occurrences. isWordByte's boundary check does not exclude '.' and '(', so
// bare "done" in "I renamed done() to finish()" or "wg.done() missing its
// defer" previously matched and burned the run-level first-declaration slot
// on a non-claim (#364).
func sdIndexDone(lowerText string) int {
	for from := 0; ; {
		rel := sdIndexWord(lowerText[from:], "done")
		if rel < 0 {
			return -1
		}
		i := from + rel
		end := i + len("done")
		// "done(" / "wg.done()" — identifier usage, not a completion claim.
		if end < len(lowerText) && lowerText[end] == '(' {
			from = end
			continue
		}
		if i > 0 && lowerText[i-1] == '.' {
			from = end
			continue
		}
		return i
	}
}

// sdContainsDeclaration checks if text contains any explicit success phrase.
func sdContainsDeclaration(lowerText string) bool {
	_, idx := sdFindDeclaration(lowerText)
	return idx >= 0
}

// sdHasCaveatWindowed checks for hedging caveats in the local context of the
// declaration (#352):
//   - BEFORE the phrase: hedging conventionally precedes the claim ("we still
//     need to write tests before this is all done") — vetoes.
//   - AFTER the phrase but in the SAME sentence ("all done, but first we need
//     tests") — vetoes.
//   - AFTER a sentence boundary (./;/newline): that is standard wrap-up
//     commentary ("All done. Note that I also refactored X.", "The task is
//     complete. However, see the docs.") — does NOT veto.
//   - Negated hedges ("no remaining issues") never veto.
func sdHasCaveatWindowed(lowerText string, declStart, declEnd int) bool {
	start := declStart - sdCaveatWindow
	if start < 0 {
		start = 0
	}
	end := declEnd + sdCaveatWindow
	if end > len(lowerText) {
		end = len(lowerText)
	}
	window := lowerText[start:end]
	for _, caveat := range sdCaveatPhrases {
		idx := strings.Index(window, caveat)
		if idx < 0 {
			continue
		}
		absIdx := start + idx // absolute position of the caveat in lowerText

		// Negation directly before the caveat ("no remaining issues",
		// "nothing remaining") turns the hedge into a completion affirmation.
		// Only THIS caveat is neutralized — other caveats and the leading-hedge
		// check below must still run. Returning false here previously skipped
		// the entire scan, so "no remaining issues, but first we need tests"
		// was treated as an unhedged declaration (#364).
		negStart := absIdx - 12
		if negStart < 0 {
			negStart = 0
		}
		trimmed := strings.TrimSpace(lowerText[negStart:absIdx])
		negated := false
		for _, neg := range []string{"no", "not", "nothing", "none", "without", "zero"} {
			if strings.HasSuffix(trimmed, neg) {
				negated = true
				break
			}
		}
		if negated {
			continue
		}

		if absIdx < declStart {
			return true // leading hedge
		}
		// Trailing: veto only if still in the declaration's own sentence.
		between := lowerText[declEnd:absIdx]
		if !strings.ContainsAny(between, ".;\n") {
			return true // same-sentence hedge ("all done, but first ...")
		}
		// New-sentence wrap-up commentary: not a hedge against THIS claim.
	}
	return false
}

// sdHasCaveat checks if the FULL text contains hedging. Retained for the
// degenerate no-declaration case; windowed matching is what recordAssistantText uses.
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
