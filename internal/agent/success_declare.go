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
	// #4256 gap 1: "still work to be done: ..." is the passive-negation
	// form of "isn't done" - the sentence LITERALLY says work remains, yet
	// bare "done" matched the declaration table and the well-calibrated
	// agent got a premature-success warning for telling the truth. As a
	// CAVEAT (not negation: the lookbehind window ends before "done", so
	// phrases containing "done" can never match there) the leading "to
	// be" hedges the declaration in the windowed scan.
	"to be done", "to be finished", "to be completed",
	// #4256 gap 2: verification-intent framing. "Let me verify if the fix
	// works" matched the bare "the fix works" declaration with no caveat
	// veto - the verify framing means the outcome is UNKNOWN, the opposite
	// of a completion claim.
	"let me verify", "verify if", "verifying if", "verifying whether",
	"check if", "checking if", "let me check",
	// #4401: "make sure" is the same verification-intent framing in a
	// different verb suit. "Running tests to make sure everything is done."
	// matched bare "done" with no caveat in the window - the sentence says
	// verification is IN PROGRESS, and it also burned the run's single
	// first-declaration slot (#364).
	"make sure", "making sure",
}

// sdVerifyIntentPhrases are the caveat phrases that keep their force
// ACROSS a sentence boundary (#4521): "All done. Let me check the build."
// declares completion in sentence one and announces verification NOT done
// in sentence two - the outcome is unknown, so the veto must survive the
// period. Ordinary hedges ("however", "but first") keep their old
// same-sentence-only scope.
var sdVerifyIntentPhrases = map[string]bool{
	"let me verify":     true,
	"verify if":         true,
	"verifying if":      true,
	"verifying whether": true,
	"check if":          true,
	"checking if":       true,
	"let me check":      true,
	"make sure":         true,
	"making sure":       true,
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
	// #1499 case C: continuation openers veto even across the sentence
	// boundary (see sdContinuationPhrases).
	if sdHasContinuationWindowed(lower, declIdx+len(declPhrase)) {
		return
	}

	// #1499 case B: NEGATION on the declaration itself inverts its meaning
	// - "This part isn't done - I'll continue" hits bare "done" (word
	// boundary passes) with no caveat, was recorded as a COMPLETION claim,
	// and the follow-up work then "proved" it premature. #364 fixed
	// negation only on the caveat side. A short lookbehind window before
	// the phrase now vetoes negated declarations.
	if sdHasNegationBefore(lower, declIdx) {
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

// sdNegationPatterns mark text directly contradicting a completion claim
// within the lookbehind window before the declaration phrase.
var sdNegationPatterns = []string{
	"isn't done", "not done", "aren't done", "not finished",
	"isn't complete", "not complete", "isn't working", "not working",
	"isn't fixed", "not fixed", "isn't resolved", "not resolved",
	"nothing is done", "not yet", "isn't ready", "not ready",
}

// sdHasNegationBefore reports whether a negation appears in the window
// immediately before the declaration phrase (#1499 case B).
func sdHasNegationBefore(lowerText string, declIdx int) bool {
	const lookbehind = 48
	start := declIdx - lookbehind
	if start < 0 {
		start = 0
	}
	window := lowerText[start:declIdx]
	for _, n := range sdNegationPatterns {
		from := 0
		for {
			idx := strings.Index(window[from:], n)
			if idx < 0 {
				break
			}
			idx += from
			// #4539: a negation only vetoes the declaration when it sits
			// in the SAME sentence - "We fixed the bug that was not working.
			// The task is done." is a legitimate claim (the not-working bug
			// belongs to the previous sentence), not a negated one. The
			// trailing caveat branch has always had this boundary check; the
			// lookbehind window was a bare Contains, so cross-sentence
			// negations killed the run's only declaration.
			if !strings.ContainsAny(window[idx+len(n):], ".;\n") {
				return true
			}
			from = idx + 1
		}
	}
	return false
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
//
// sdContinuationPhrases announce follow-up work in the same breath as a
// component-level claim ("The fix works. Next, I'll clean up the tests.").
// #1499 case C: they sit AFTER the sentence boundary, so the #352
// trailing-veto and the caveat table both missed them (double miss) - the
// announced wrap-up then counted as evidence of a premature TASK-level
// declaration. Unlike hedging caveats, these veto ACROSS the sentence
// boundary: a continuation opener always means more work is coming.
var sdContinuationPhrases = []string{
	"next,",
	"next up",
	"then i",
	"now let's",
	"now lets",
	"moving on",
	"after that",
}

// sdHasContinuationWindowed reports whether a continuation opener appears
// within the caveat window after the declaration, sentence boundary or not.
func sdHasContinuationWindowed(lowerText string, declEnd int) bool {
	end := declEnd + sdCaveatWindow
	if end > len(lowerText) {
		end = len(lowerText)
	}
	after := lowerText[declEnd:end]
	for _, c := range sdContinuationPhrases {
		if sdPhraseAtWordBoundary(after, c) {
			return true
		}
	}
	return false
}

// sdPhraseAtWordBoundary reports whether phrase occurs in s at least once
// with non-word characters (or the string edges) on both sides. #4522:
// "then i" as a bare substring also matched "then in"/"then it"/"then
// is"/"then if" - high-frequency summary phrasing that wrongly vetoed
// legitimate wrap-up declarations. A hit glued to surrounding letters
// ("then insurance") is a different word and does not count.
func sdPhraseAtWordBoundary(s, phrase string) bool {
	isWord := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
	}
	for from := 0; ; {
		idx := strings.Index(s[from:], phrase)
		if idx < 0 {
			return false
		}
		idx += from
		beforeOK := idx == 0 || !isWord(s[idx-1])
		afterIdx := idx + len(phrase)
		afterOK := afterIdx >= len(s) || !isWord(s[afterIdx])
		if beforeOK && afterOK {
			return true
		}
		from = idx + 1
	}
}

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
			// #4539: leading hedges only veto within the declaration's own
			// sentence, mirroring the trailing branch. "However, see caveats
			// below. The task is complete." is the documented wrap-up shape
			// (L79-83) - the caveat lives in its own previous sentence and
			// must not kill the claim.
			caveatEnd := absIdx + len(caveat)
			if caveatEnd >= declStart {
				// #4256: the caveat phrase OVERLAPS the declaration itself
				// ("still work to be done" vs the bare "done" declaration) -
				// same sentence by construction, and the hedging framing is
				// the whole point of that table entry.
				return true
			}
			between := lowerText[caveatEnd:declStart]
			if !strings.ContainsAny(between, ".;\n") {
				return true // same-sentence leading hedge
			}
			// Cross-sentence leading hedge: not a hedge against THIS claim.
			continue
		}
		// Trailing: veto only if still in the declaration's own sentence.
		between := lowerText[declEnd:absIdx]
		if !strings.ContainsAny(between, ".;\n") {
			return true // same-sentence hedge ("all done, but first ...")
		}
		// #4521: verification-intent framing survives the sentence break.
		// "All done. Let me check the build." is not a wrapped-up claim with
		// commentary after it - the second sentence announces the verification
		// has NOT happened yet, which is exactly the #4256 gap-2 intent.
		if sdVerifyIntentPhrases[caveat] {
			return true
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

// markUndelivered (#4224) rolls back the fire marker consumed by maybeWarn
// when the guidance budget suppressed delivery (#3380 rollback pattern).
func (s *successDeclareState) markUndelivered() {
	s.fired = false
	if s.warnCount > 0 {
		s.warnCount--
	}
}
