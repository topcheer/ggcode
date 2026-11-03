package agent

import (
	"strings"
	"unicode"
)

const (
	rrWindow        = 3
	rrMinWords      = 30
	rrSimilarityTh  = 0.55
	rrMaxWarnings   = 2
	rrMinTotalWords = 80
)

type rrTurn struct {
	words    map[string]bool
	wordList []string
	rawLen   int
}

type reasoningRedundancyState struct {
	turns     []rrTurn
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

func (s *reasoningRedundancyState) recordReasoning(text string, toolCalled bool) {
	if toolCalled {

		s.turns = s.turns[:0]
		return
	}

	words := tokenizeForRR(text)
	if len(words) < rrMinWords {

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

	if len(s.turns) > rrWindow+1 {
		s.turns = s.turns[1:]
	}
}

func (s *reasoningRedundancyState) markUndelivered() {
	if s.warnCount > 0 {
		s.warnCount--
	}
	if s.totalFire > 0 {
		s.totalFire--
	}
}

func (s *reasoningRedundancyState) maybeWarn(iter, maxIter int) string {
	if s.warnCount >= rrMaxWarnings {
		return ""
	}
	if len(s.turns) < rrWindow {
		return ""
	}

	window := s.turns[len(s.turns)-rrWindow:]

	totalWords := 0
	for _, t := range window {
		totalWords += len(t.wordList)
	}
	if totalWords < rrMinTotalWords {
		return ""
	}

	similarPairs := 0
	for i := 0; i < len(window)-1; i++ {
		if jaccardSimilarity(window[i].words, window[i+1].words) >= rrSimilarityTh {
			similarPairs++
		}
	}

	if similarPairs < rrWindow-1 {
		return ""
	}

	s.warnCount++
	s.totalFire++

	remaining := maxIter - iter
	return "[reasoning-redundancy] " + itoaRR(rrWindow) + " analysis-only turns with " +
		itoaRR(int(rrSimilarityTh*100)) + "%+ overlap. Stop deliberating and act. (" + itoaRR(remaining) + " iters left)"
}

func tokenizeForRR(text string) []string {

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
