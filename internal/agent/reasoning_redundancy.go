package agent

import (
	"strings"
	"unicode"
)

// Reasoning Redundancy Detector
//
// Research basis:
//   - "Stop Overthinking: A Survey on Efficient Reasoning for Large Language
//     Models" (arXiv:2503.16419, TMLR 2025): identifies "solution redundancy"
//     and "over-detailed reasoning" as two of the three core overthinking
//     patterns. Solution redundancy occurs when an LRM re-derives or
//     re-explains the same reasoning steps across multiple turns without
//     producing new conclusions or actions.
//   - "Do NOT Think That Much: Characterizing the Overthinking Behavior of
//     Large Reasoning Models" (Chen et al., arXiv:2410.12379, 2024): shows
//     that LVLMs/LRMs frequently emit near-duplicate reasoning content across
//     solution attempts, wasting inference compute without quality gains.
//   - "AgentTTS: Test-time Compute-optimal Scaling" (arXiv:2508.00890, 2025):
//     demonstrates that multi-stage agent tasks suffer from redundant
//     "re-planning" iterations where the agent re-assesses the situation
//     identically without taking actionable steps.
//
// Problem: AI coding agents sometimes "overthink" by producing substantial
// reasoning text (analysis, planning, explanation) that is near-duplicate
// across consecutive iterations WITHOUT taking any tool action. This wastes
// context window and inference tokens without forward progress. Unlike tool
// storm (too much action, no reasoning), this is the inverse: too much
// reasoning, no action, and the reasoning is redundant.
//
// Example failure pattern:
//   Iter N:   "Let me analyze the structure. The handler processes requests
//             by routing them to the service layer. I should check how
//             errors propagate..." (120 words, no tool call)
//   Iter N+1: "Looking at the structure, the handler routes requests to the
//             service layer. I need to understand error propagation here..."
//             (115 words, no tool call)
//   Iter N+2: "The handler processes requests and routes them to services.
//             Error propagation should be examined..." (108 words, no tool call)
//   → 3 consecutive text-heavy iterations with no tool calls, ~80% semantic
//     overlap between adjacent turns. Pure overthinking.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - tool_storm_detect.go: detects too many tools without reasoning.
//     This is the INVERSE pattern (too much reasoning without tools).
//   - convergence_lock.go: detects repeated tool calls without progress.
//     Requires tool calls to trigger; pure-reasoning iterations are skipped.
//   - ungrounded_reflection.go: detects reflection not tied to tool output.
//     Checks single-iteration grounding, not cross-iteration redundancy.
//   - circular_reasoning.go: detects circular logical patterns within text.
//     Checks intra-text logic, not inter-text semantic overlap.
//   - mindless_action.go: detects actions without sufficient deliberation.
//     Opposite direction -- not enough thinking, not too much.
//
// Gap: No detector identifies the pattern where the agent produces
// near-duplicate substantial reasoning text across consecutive iterations
// without taking any tool action. This detector fills that gap by computing
// Jaccard word-set similarity between consecutive text-only turns.
//
// Design:
//   - After each iteration, records the assistant's text (if substantial)
//     and whether a tool was called.
//   - Maintains a sliding window of the last N text-only reasoning turns.
//   - Triggers when: last K iterations were all text-only (no tool call)
//     AND pairwise Jaccard similarity between consecutive turns exceeds
//     threshold AND total reasoning text is substantial.
//   - Non-blocking: injects guidance nudge to stop deliberating and act.
//   - Fires at most `maxWarnings` times per run to avoid nagging.
//   - Zero LLM cost (deterministic token-set overlap computation).

const (
	rrWindow        = 3    // consecutive text-only turns to examine
	rrMinWords      = 30   // minimum words in a turn to count as "substantial reasoning"
	rrSimilarityTh  = 0.55 // Jaccard word-set overlap threshold for "redundant"
	rrMaxWarnings   = 2    // max nudges per run
	rrMinTotalWords = 80   // minimum combined words across window to fire
)

type rrTurn struct {
	words    map[string]bool
	wordList []string // for overlap analysis
	rawLen   int
}

type reasoningRedundancyState struct {
	turns     []rrTurn // text-only turns (no tool call in that iteration)
	warnCount int
	totalFire int
}

func newReasoningRedundancyState() *reasoningRedundancyState {
	return &reasoningRedundancyState{
		turns: make([]rrTurn, 0, rrWindow+1),
	}
}

func (s *reasoningRedundancyState) reset() {
	s.turns = s.turns[:0]
	s.warnCount = 0
	s.totalFire = 0
}

// recordReasoning records the assistant text for an iteration. If a tool was
// called this iteration, the window is cleared (tool action breaks the
// pure-deliberation chain).
func (s *reasoningRedundancyState) recordReasoning(text string, toolCalled bool) {
	if toolCalled {
		// Any tool call breaks the text-only streak
		s.turns = s.turns[:0]
		return
	}

	words := tokenizeForRR(text)
	if len(words) < rrMinWords {
		// Too short to count as "substantial overthinking"
		s.turns = s.turns[:0]
		return
	}

	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}

	s.turns = append(s.turns, rrTurn{
		words:    wordSet,
		wordList: words,
		rawLen:   len(text),
	})

	// Keep only last rrWindow+1 turns
	if len(s.turns) > rrWindow+1 {
		s.turns = s.turns[1:]
	}
}

// maybeWarn checks if the current window exhibits redundant overthinking.
// Returns a guidance message if the pattern is detected, "" otherwise.
func (s *reasoningRedundancyState) maybeWarn(iter, maxIter int) string {
	if s.warnCount >= rrMaxWarnings {
		return ""
	}
	if len(s.turns) < rrWindow {
		return ""
	}

	// Need at least rrWindow consecutive text-only turns
	window := s.turns[len(s.turns)-rrWindow:]

	// Check total word volume across the window
	totalWords := 0
	for _, t := range window {
		totalWords += len(t.wordList)
	}
	if totalWords < rrMinTotalWords {
		return ""
	}

	// Compute pairwise Jaccard similarity between consecutive turns
	similarPairs := 0
	for i := 0; i < len(window)-1; i++ {
		if jaccardSimilarity(window[i].words, window[i+1].words) >= rrSimilarityTh {
			similarPairs++
		}
	}

	// Require at least (rrWindow-1) similar pairs -- i.e., all adjacent
	// pairs are redundant
	if similarPairs < rrWindow-1 {
		return ""
	}

	s.warnCount++
	s.totalFire++

	remaining := maxIter - iter
	return "[reasoning-redundancy] " + itoaRR(rrWindow) + " analysis-only turns with " +
		itoaRR(int(rrSimilarityTh*100)) + "%+ overlap. Stop deliberating and act. (" + itoaRR(remaining) + " iters left)"
}

// tokenizeForRR splits text into normalized lowercase word tokens for
// redundancy comparison. Strips punctuation and normalizes whitespace.
// Note: jaccardSimilarity is reused from prompt_ops.go.
func tokenizeForRR(text string) []string {
	// Pre-allocate based on rough estimate
	est := len(text) / 6
	if est < 16 {
		est = 16
	}
	tokens := make([]string, 0, est)

	var sb strings.Builder
	sb.Grow(32)

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else if sb.Len() > 0 {
			tokens = append(tokens, sb.String())
			sb.Reset()
		}
	}
	if sb.Len() > 0 {
		tokens = append(tokens, sb.String())
	}
	return tokens
}

// itoaRR is a local int-to-string to avoid import conflicts.
func itoaRR(n int) string {
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
