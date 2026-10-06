package cost

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Pre-execution token/cost forecasting (r434). Stanford Digital Economy
// study "How Do AI Agents Spend Your Money?" shows task token consumption
// is predictable from task shape. ggcode records every past run's usage in
// session JSONL; this package turns those history samples into a
// before-you-run estimate: pure statistics, zero LLM cost.

// RunSample is one historical run observation: the first user prompt that
// started the run, and the total tokens the run actually consumed.
type RunSample struct {
	FirstPrompt string
	Tokens      int
}

// Forecast is the prediction output: a percentile band over the token
// totals of the K most similar historical runs.
type Forecast struct {
	// Neighbors is the number of similar historical runs the band is built
	// from (after excluding zero-token runs).
	Neighbors int

	// Median is the P50 token total of the neighbors.
	Median int

	// Low / High are the P25 / P75 token totals - the "likely range".
	Low  int
	High int

	// Degraded reports that fewer than minNeighbors usable runs existed in
	// total, so the band rests on a thin sample (nearest-rank over all
	// runs). Note KNN still picks the most similar runs when absolute
	// similarity is low - the band is always the closest evidence
	// available, and Degraded flags only sample-size weakness.
	// Callers should surface this honesty cue.
	Degraded bool
}

const (
	// forecastK is how many most-similar historical runs form the band.
	forecastK = 8

	// minNeighbors is the similar-run count below which the forecast
	// degrades to the global distribution (a 2-run band is noise).
	minNeighbors = 5

	// promptWordCap bounds the token set per prompt so a pathological
	// first prompt cannot blow up the similarity computation.
	promptWordCap = 64
)

// ForecastTokens predicts the token band for a new task prompt from
// historical run samples. Pure function: same inputs, same output.
//
// Similarity is deliberately coarse - first-prompt length bucket (agents
// asked to "fix a typo" and "refactor the parser" burn very different
// tokens, and length is the cheapest observable proxy for task scope)
// plus keyword-set Jaccard overlap. This is a forecast, not a search
// engine; the goal is an honest band, not a perfect match.
func ForecastTokens(samples []RunSample, prompt string) Forecast {
	var f Forecast
	usable := 0
	for _, s := range samples {
		if s.Tokens > 0 {
			usable++
		}
	}
	if usable == 0 {
		return f
	}

	// Rank by similarity, best first.
	idx := make([]int, 0, len(samples))
	for i, s := range samples {
		if s.Tokens > 0 {
			idx = append(idx, i)
		}
	}
	qLen := lengthBucket(prompt)
	qWords := wordSet(prompt)
	sims := make([]float64, len(samples))
	for _, i := range idx {
		s := samples[i]
		sims[i] = similarity(qLen, qWords, lengthBucket(s.FirstPrompt), wordSet(s.FirstPrompt))
	}
	sort.Slice(idx, func(a, b int) bool {
		if sims[idx[a]] != sims[idx[b]] {
			return sims[idx[a]] > sims[idx[b]]
		}
		return samples[idx[a]].Tokens < samples[idx[b]].Tokens // stable tie-break
	})

	if len(idx) > forecastK {
		idx = idx[:forecastK]
	}

	// With too few similar neighbors, widen to the global distribution.
	if len(idx) < minNeighbors {
		f.Degraded = true
		idx = idx[:0]
		for i, s := range samples {
			if s.Tokens > 0 {
				idx = append(idx, i)
			}
		}
	}

	tokens := make([]int, len(idx))
	for j, i := range idx {
		tokens[j] = samples[i].Tokens
	}
	sort.Ints(tokens)

	f.Neighbors = len(tokens)
	f.Low = percentile(tokens, 25)
	f.Median = percentile(tokens, 50)
	f.High = percentile(tokens, 75)
	return f
}

// similarity combines length-bucket proximity and keyword-set Jaccard into
// a single score in [0,1].
func similarity(qLen int, qWords map[string]bool, sLen int, sWords map[string]bool) float64 {
	lenScore := 0.0
	d := qLen - sLen
	if d < 0 {
		d = -d
	}
	if d == 0 {
		lenScore = 1
	} else {
		lenScore = 1.0 / float64(d+1) // adjacent buckets still count a bit
	}
	return 0.5*lenScore + 0.5*jaccard(qWords, sWords)
}

// lengthBucket maps prompt length to a log2 bucket: 0=empty, 1=1 char,
// 2=2-3, 3=4-7, ... Tasks an order of magnitude apart in prompt size land
// in different buckets.
func lengthBucket(p string) int {
	n := len(p)
	if n == 0 {
		return 0
	}
	return int(math.Floor(math.Log2(float64(n)))) + 1
}

// wordSet lowercases and splits a prompt into a bounded set of
// alphanumeric words.
func wordSet(p string) map[string]bool {
	set := make(map[string]bool)
	if p == "" {
		return set
	}
	fields := strings.FieldsFunc(p, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range fields {
		w = strings.ToLower(w)
		if len(w) < 2 { // drop 1-char noise
			continue
		}
		set[w] = true
		if len(set) >= promptWordCap {
			break
		}
	}
	return set
}

// jaccard is set-overlap similarity in [0,1]; empty-vs-empty is 0 (no
// evidence either way).
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	if inter == 0 {
		return 0
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// percentile returns the p-th percentile of a sorted-nonempty int slice
// using nearest-rank interpolation.
func percentile(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := float64(p) / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := pos - float64(lo)
	return int(float64(sorted[lo])*(1-frac) + float64(sorted[hi])*frac + 0.5)
}
