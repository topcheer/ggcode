package agent

// Spiral of Hallucination Detector (Cross-Turn Epistemic Error Propagation)
//
// Research basis:
//   - Zhang et al., "Agentic Uncertainty Quantification" (arXiv:2601.15703,
//     January 2026, Salesforce AI Research). Introduces the "Spiral of
//     Hallucination" -- when an agent makes an epistemic error at step t
//     (e.g., an unverified assumption, a hedged guess), and that error
//     becomes part of the context for step t+1, it transforms from an
//     internal cognitive deficiency into an external "ground truth"
//     constraint. All subsequent planning is biased toward an irreversible
//     failure state. The key insight is that epistemic and aleatoric
//     uncertainty are NOT independent in long trajectories: committing an
//     epistemic error to history converts it into a false premise for future
//     steps.
//
//   - Duan et al., "UProp: Uncertainty Propagation in Multi-Step Agentic
//     Decision-Making" (arXiv:2506.17419, 2025). Mathematically proves
//     how local epistemic errors compound into global trajectory failures.
//
//   - Kalai et al., "Why Language Models Hallucinate" (arXiv:2509.04664,
//     2025). Shows that hallucination in multi-turn settings is primarily
//     driven by the inability to distinguish earlier speculative content
//     from grounded fact.
//
// Problem: ggcode has many detectors for epistemic uncertainty WITHIN a
// single assistant turn (assumption_track, action_hedging, compounded_
// uncertainty). But the Spiral of Hallucination is a CROSS-TURN phenomenon:
//
//  Turn 1: "I assume the database is PostgreSQL"   → assumption expressed
//  Turn 3: "Since we're using PostgreSQL, I'll..." → assumption now treated
//           as confirmed fact, no verification occurred
//  Turn 5: "The PostgreSQL connection pool needs"  → fully committed to an
//           unverified foundation
//
// The spiral occurs when the agent's own expressed uncertainty at turn N
// is subsequently treated as established fact at turn N+2, N+3, etc.,
// WITHOUT any intervening verification step (test run, confirmation,
// explicit check). Each turn deepens the commitment to the unverified
// foundation.
//
// What this detector does that others DON'T:
//   - compounded_uncertainty.go: counts the TOTAL number of epistemic
//     events (cumulative). This detector tracks the TEMPORAL PATTERN of
//     uncertainty-then-commitment -- specifically, whether the agent
//     LATER treats a previously-expressed uncertainty as established fact.
//   - assumption_track.go: detects assumptions within one turn. This
//     detector detects the OPPOSITE direction -- when an assumption from
//     a prior turn is acted upon as fact in a later turn.
//   - false_premise.go: detects when an agent's initial premise is wrong
//     relative to codebase evidence. This detector detects when an agent's
//     OWN prior uncertainty (regardless of whether it was right or wrong)
//     is later treated as confirmed without verification.
//
// Design:
//   - Records turns where the agent expresses epistemic uncertainty
//     (assumptions, hedging, speculative language) and extracts topic
//     keywords from those turns
//   - In subsequent turns, detects when the agent uses assertive/committed
//     language about those same topics (signaling the prior uncertainty
//     has been silently promoted to "fact")
//   - Triggers when the spiral pattern is detected: prior-uncertainty →
//     later-commitment on the same topic without intervening verification
//   - Zero LLM cost -- pure deterministic text pattern matching + keyword
//     overlap
//   - Fires at most once per run (advisory, non-blocking)
//   - Resets on new user turn

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// spiralMaxWarnings: max warnings per run.
	spiralMaxWarnings = 1

	// spiralMinGap: minimum turns between uncertainty and commitment
	// for it to count as a spiral. Same-turn would just be inconsistency.
	spiralMinGap = 2

	// spiralMinTopicLen: minimum keyword length to track (avoid noise).
	spiralMinTopicLen = 4

	// spiralMaxTopics: maximum topics to track from uncertainty turns.
	spiralMaxTopics = 15

	// spiralMinCommittedTurns: how many subsequent committed statements
	// about an unverified topic before triggering.
	spiralMinCommittedTurns = 2
)

// spiralUncertaintyPattern matches language where the agent expresses
// epistemic uncertainty about a specific topic.
var spiralUncertaintyRe = regexp.MustCompile(`(?i)(?:I assume|assuming|I'm assuming|presumably|I think|probably|likely|my guess|if I had to guess|it seems like|appears to be|I believe this|without knowing|best guess|might be|could be)\b`)

// spiralCommittedRe matches language where the agent treats a topic as
// established fact (no hedging, asserting certainty).
var spiralCommittedRe = regexp.MustCompile(`(?i)(?:since we(?:'re| are)|because (?:the|this|we)|given that|now that|as (?:we|the)|so (?:the|this|we)|therefore|this means|which means|the \w+ (?:is|was|has|uses|requires|needs))\b`)

// spiralVerificationRe detects that a verification step occurred. Kept
// tightly scoped to explicit first-person assertion phrases ONLY as a
// fallback — generic words like test/build/error/result appear in nearly
// every assistant narrative and previously disabled the detector on any
// match (#161). The primary signal is the real tool-call event recorded
// by recordSpiralVerification from the tool execution loop.
var spiralVerificationRe = regexp.MustCompile(`(?i)(?:i (?:have |'ve )?(?:verified|confirmed|validated)\b|i ran (?:the )?(?:test|build|lint|check)s?\b)`)

// recordSpiralVerification marks that a real tool call with observable
// results occurred this turn (fix #161: prose keyword matching silently
// disabled the detector in most runs).
func (a *Agent) recordSpiralVerification() {
	if a == nil || a.spiralState == nil {
		return
	}
	a.spiralState.verified = true
}

// spiralTopicWordRe extracts candidate topic keywords from text near
// uncertainty markers. Looks for nouns/technical terms.
var spiralTopicWordRe = regexp.MustCompile(`\b([a-z][a-z0-9_-]{3,30})\b`)

// Common stop words to exclude from topic extraction.
var spiralStopWords = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "have": true,
	"will": true, "been": true, "they": true, "them": true, "what": true,
	"when": true, "were": true, "more": true, "some": true, "than": true,
	"then": true, "into": true, "only": true, "your": true, "which": true,
	"their": true, "would": true, "could": true, "should": true, "about": true,
	"there": true, "where": true, "after": true, "before": true, "being": true,
	"these": true, "those": true, "under": true, "still": true,
	"make": true, "made": true, "just": true, "also": true, "need": true,
	"here": true, "very": true, "like": true, "even": true, "most": true,
	"many": true, "much": true, "such": true, "same": true, "other": true,
	"first": true, "last": true, "next": true, "over": true, "does": true,
	"done": true, "case": true, "each": true, "both": true, "want": true,
	"look": true, "came": true, "back": true, "way": true, "part": true,
	"give": true, "once": true, "upon": true, "help": true, "work": true,
	"going": true, "left": true, "used": true, "uses": true,
	"probably": true, "might": true, "think": true, "guess": true,
	"assume": true, "assuming": true, "seems": true, "appears": true,
	"without": true, "knowing": true, "likely": true, "presumably": true,
	"since": true, "because": true, "given": true, "therefore": true,
}

// spiralTrackedTopic represents a topic from a turn where uncertainty
// was expressed, tracked for later committed usage.
type spiralTrackedTopic struct {
	word       string
	sourceTurn int
}

// spiralHallucinationState tracks the cross-turn spiral pattern.
type spiralHallucinationState struct {
	turn            int
	warnings        int
	topics          []spiralTrackedTopic // topics from prior uncertainty turns
	committedCounts map[string]int       // topic -> count of committed assertions
	verified        bool                 // whether a verification step occurred since last uncertainty
}

func newSpiralHallucinationState() *spiralHallucinationState {
	return &spiralHallucinationState{
		committedCounts: make(map[string]int),
	}
}

func (s *spiralHallucinationState) reset() {
	s.turn = 0
	s.warnings = 0
	s.topics = nil
	s.committedCounts = make(map[string]int)
	s.verified = false
}

// extractSpiralTopics pulls candidate topic keywords from text segments
// near uncertainty markers. Returns deduplicated lowercase keywords.
func extractSpiralTopics(text string) []string {
	var topics []string
	seen := make(map[string]bool)

	// Find all uncertainty markers and extract nearby words
	locs := spiralUncertaintyRe.FindAllStringIndex(text, -1)
	for _, loc := range locs {
		// Look at a window after the uncertainty marker (where the topic
		// being hedged usually appears)
		windowStart := loc[1]
		windowEnd := windowStart + 200
		if windowEnd > len(text) {
			windowEnd = len(text)
		}
		window := strings.ToLower(text[windowStart:windowEnd])

		words := spiralTopicWordRe.FindAllString(window, -1)
		for _, w := range words {
			if len(w) < spiralMinTopicLen {
				continue
			}
			if spiralStopWords[w] {
				continue
			}
			if seen[w] {
				continue
			}
			seen[w] = true
			topics = append(topics, w)
			if len(topics) >= spiralMaxTopics {
				return topics
			}
		}
	}
	return topics
}

// detectCommittedTopics finds topics from prior uncertainty turns that
// appear in committed/assertive language in the current text.
func detectCommittedTopics(text string, tracked []spiralTrackedTopic) []string {
	if len(tracked) == 0 {
		return nil
	}

	lowerText := strings.ToLower(text)
	var matched []string

	// Check if any committed language appears in the text
	hasCommitted := spiralCommittedRe.MatchString(lowerText)
	if !hasCommitted {
		return nil
	}

	// Find which tracked topics appear in the text
	for _, t := range tracked {
		if strings.Contains(lowerText, t.word) {
			// Make sure this isn't itself an uncertainty turn (no hedging
			// about this topic in the same text)
			if spiralUncertaintyRe.MatchString(lowerText) {
				// Check if the uncertainty is specifically about this topic
				// by looking for the topic near an uncertainty marker
				stillHedging := false
				locs := spiralUncertaintyRe.FindAllStringIndex(lowerText, -1)
				for _, loc := range locs {
					windowEnd := loc[1] + 100
					if windowEnd > len(lowerText) {
						windowEnd = len(lowerText)
					}
					window := lowerText[loc[1]:windowEnd]
					if strings.Contains(window, t.word) {
						stillHedging = true
						break
					}
				}
				if stillHedging {
					continue
				}
			}
			matched = append(matched, t.word)
		}
	}
	return matched
}

// recordSpiralTurn processes one assistant turn: records uncertainty topics,
// checks for committed assertions about prior topics, and detects verification.
func (a *Agent) recordSpiralTurn(text string) {
	if a.spiralState == nil {
		return
	}
	s := a.spiralState
	s.turn++

	// Check if a verification step occurred — either a real tool call with
	// observable results (recorded by recordSpiralVerification from the tool
	// loop) or an explicit first-person verification assertion (#161:
	// generic keyword matching on prose disabled the detector far too
	// easily).
	if spiralVerificationRe.MatchString(text) {
		s.verified = true
	}

	// Extract topics from any uncertainty expressed in this turn
	newTopics := extractSpiralTopics(text)
	if len(newTopics) > 0 {
		for _, t := range newTopics {
			s.topics = append(s.topics, spiralTrackedTopic{
				word:       t,
				sourceTurn: s.turn,
			})
		}
		// New uncertainty resets the verification flag -- the agent is
		// expressing new doubts
		s.verified = false
	}

	// Check for committed assertions about previously uncertain topics
	if len(s.topics) > 0 && !s.verified {
		committed := detectCommittedTopics(text, s.topics)
		for _, topic := range committed {
			s.committedCounts[topic]++
		}
	}
}

// maybeWarnSpiralHallucination checks if the spiral pattern has been
// detected: the agent is committing to previously-uncertain foundations
// without verification. Returns guidance if the spiral threshold is crossed.
func (a *Agent) maybeWarnSpiralHallucination() string {
	if a.spiralState == nil || a.spiralState.warnings >= spiralMaxWarnings {
		return ""
	}
	s := a.spiralState

	// Find topics that have accumulated enough committed assertions
	var spiraled []string
	for topic, count := range s.committedCounts {
		if count >= spiralMinCommittedTurns {
			spiraled = append(spiraled, topic)
		}
	}

	if len(spiraled) == 0 {
		return ""
	}

	// Filter out topics that were verified since their uncertainty was expressed
	if s.verified {
		// Verification happened -- the spiral is broken for topics verified
		// after their uncertainty turn. But we can't easily distinguish which
		// topics were verified. Be conservative: only warn if 3+ topics
		// spiraled AND no verification in the most recent turns.
		if len(spiraled) < 3 {
			return ""
		}
	}

	s.warnings++

	// Limit examples in the message
	if len(spiraled) > 5 {
		spiraled = spiraled[:5]
	}

	return fmt.Sprintf(
		"[spiral-of-hallucination] You expressed uncertainty about certain "+
			"topics in earlier turns, but are now treating them as established "+
			"fact without verification. Topics spiraling: %s.\n"+
			"This is the 'Spiral of Hallucination' pattern (arXiv:2601.15703): "+
			"early epistemic errors, once committed to context, propagate "+
			"irreversibly -- each subsequent turn deepens the commitment to an "+
			"unverified foundation. The agent's own hedged guesses from turn N "+
			"become treated as confirmed ground truth by turn N+2.\n"+
			"Before continuing:\n"+
			"1. Identify which prior assumptions still need verification\n"+
			"2. Run a test, diagnostic, or check to confirm or refute the "+
			"uncertain foundations\n"+
			"3. If an assumption is wrong, course-correct NOW before the "+
			"error propagates further into your edits\n"+
			"If the foundations have already been verified (test passed, "+
			"diagnostic confirmed), this warning can be safely ignored.",
		strings.Join(spiraled, ", "),
	)
}
