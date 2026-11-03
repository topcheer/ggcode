package tool

import (
	"strings"
	"unicode"
)

const maxFuzzyResults = 3

func suggestSkills(query string, allNames []string) []string {
	query = normalizeSkillName(query)
	if query == "" || len(allNames) == 0 {
		return nil
	}

	type candidate struct {
		name string
		dist int
	}
	candidates := make([]candidate, 0, len(allNames))
	for _, n := range allNames {
		norm := normalizeSkillName(n)
		if norm == "" {
			continue
		}

		if norm == query {
			candidates = append(candidates, candidate{name: n, dist: 0})
			continue
		}

		if strings.Contains(norm, query) || strings.Contains(query, norm) {
			candidates = append(candidates, candidate{name: n, dist: -1})
			continue
		}
		d := levenshtein(query, norm)

		threshold := len(query) / 3
		if threshold < 1 {
			threshold = 1
		}
		if threshold > 4 {
			threshold = 4
		}
		if d <= threshold {
			candidates = append(candidates, candidate{name: n, dist: d})
		}
	}

	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if rank(candidates[j].dist) < rank(candidates[i].dist) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	limit := maxFuzzyResults
	if len(candidates) < limit {
		limit = len(candidates)
	}
	result := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, candidates[i].name)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeSkillName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func rank(dist int) int {
	if dist == 0 {
		return 0
	}
	if dist == -1 {
		return 1
	}
	return dist + 2
}
