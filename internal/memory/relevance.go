package memory

// Task-relevance gate for persistent-memory inline injection (sa-113).
//
// Frontier basis: Self-RAG (Asai et al., ICLR 2024) reflection-token
// [Retrieve]/[IsRel] decisions and SPARKLE (ACL 2026) Retrieval Decision
// Agent - an agent should decide whether retrieved knowledge is relevant
// to the CURRENT task before letting it occupy context. ggcode's
// experience channel already gates on lexical relevance
// (experience.go score>=0.5, minDistinct>=2); the auto-memory channel
// inlined EVERY active persistent entry into every system prompt with no
// task signal at all (loadForPrompt took no task argument), so a sub-agent
// spawned for one focused task still paid for - and was distracted by -
// every standing memory of the workspace.
//
// This gate is the deterministic (zero-LLM) IsRel equivalent for the
// memory channel: entries below the relevance threshold degrade from
// inline to index-only (the key list stays, read_file still works - the
// retrieval loop stays closed). Task=="" bypasses the gate entirely,
// preserving the standing-rules semantics of interactive sessions.

import "math"

// relCandidate is one persistent-entry candidate for relevance scoring.
type relCandidate struct {
	key     string
	content string
}

// relevanceGate holds the IDF statistics for one scoring pass.
type relevanceGate struct {
	qSet        map[string]int
	df          map[string]int
	n           float64
	minDistinct int
}

// newRelevanceGate builds the gate: task tokens vs the document frequency
// across all persistent candidates. Returns nil when the task carries no
// usable tokens (gate disabled - everything stays relevant).
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

// relevant scores one entry with the same BM25-lite weighting the
// experience channel uses (idf * tf-saturation * query-term cap): two
// distinct shared tokens, or one shared rare token, clear the bar.
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
