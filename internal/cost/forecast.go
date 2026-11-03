package cost

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type RunSample struct {
	FirstPrompt string
	Tokens      int
}

type Forecast struct {
	Neighbors int

	Median int

	Low  int
	High int

	Degraded bool
}

const (
	forecastK = 8

	minNeighbors = 5

	promptWordCap = 64
)

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
		return samples[idx[a]].Tokens < samples[idx[b]].Tokens
	})

	if len(idx) > forecastK {
		idx = idx[:forecastK]
	}

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

func similarity(qLen int, qWords map[string]bool, sLen int, sWords map[string]bool) float64 {
	lenScore := 0.0
	d := qLen - sLen
	if d < 0 {
		d = -d
	}
	if d == 0 {
		lenScore = 1
	} else {
		lenScore = 1.0 / float64(d+1)
	}
	return 0.5*lenScore + 0.5*jaccard(qWords, sWords)
}

func lengthBucket(p string) int {
	n := len(p)
	if n == 0 {
		return 0
	}
	return int(math.Floor(math.Log2(float64(n)))) + 1
}

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
		if len(w) < 2 {
			continue
		}
		set[w] = true
		if len(set) >= promptWordCap {
			break
		}
	}
	return set
}

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
