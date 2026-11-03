package memory

import "math"

type relCandidate struct {
	key     string
	content string
}

type relevanceGate struct {
	qSet        map[string]int
	df          map[string]int
	n           float64
	minDistinct int
}

func newRelevanceGate(task string, cands []relCandidate) *relevanceGate {
	qSet := tokenSet(expTokenize(task))
	if len(qSet) == 0 {
		return nil
	}
	df := make(map[string]int)
	for _, c := range cands {
		for t := range tokenSet(expTokenize(c.key + " " + c.content)) {
			df[t]++
		}
	}
	minDistinct := 2
	if len(qSet) < 2 {
		minDistinct = 1
	}
	return &relevanceGate{qSet: qSet, df: df, n: float64(len(cands)), minDistinct: minDistinct}
}

func (g *relevanceGate) relevant(key, content string) bool {
	if g == nil {
		return true
	}
	toks := tokenSet(expTokenize(key + " " + content))
	distinct := 0
	var score float64
	for t, qCount := range g.qSet {
		tf := toks[t]
		if tf == 0 {
			continue
		}
		distinct++
		idf := math.Log(1 + g.n/float64(g.df[t]))
		score += idf * (float64(tf) / (float64(tf) + 1.2)) * float64(minInt(qCount, 3))
	}
	return distinct >= g.minDistinct && score >= 0.5
}
