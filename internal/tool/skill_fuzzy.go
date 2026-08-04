package tool

// Skill Fuzzy Matching with Suggestions
//
// Research basis: Claude Code and Cursor both provide fuzzy/did-you-mean
// matching when users or the LLM invoke skills with slightly wrong names.
// Common mismatches include:
//   - underscore vs hyphen: "browser_automation" vs "browser-automation"
//   - missing suffix: "verify" vs "verify-changes"
//   - word order: "review-changes" vs "changes-review"
//   - case differences: "Debug" vs "debug"
//
// Without suggestions, the LLM wastes an iteration on a hard error and then
// has to re-read the available skills list. With suggestions, it gets the
// correct name immediately and can retry.
//
// This uses Levenshtein edit distance (no external dependency) with a
// threshold based on input length. Normalization (lowercase, strip non-alnum)
// handles most real-world mismatches.

import (
	"strings"
	"unicode"
)

// maxFuzzyResults limits how many suggestions we return.
const maxFuzzyResults = 3

// suggestSkills returns up to maxFuzzyResults skill names that are close
// to the input query. Closeness is determined by normalized Levenshtein
// distance. Returns nil if no good matches are found.
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
		// If the normalized name exactly matches the normalized query,
		// this is a high-confidence suggestion (e.g. "browser_automation"
		// matches "browser-automation" after normalization).
		if norm == query {
			candidates = append(candidates, candidate{name: n, dist: 0})
			continue
		}
		// Check if either contains the other as a substring -- high-confidence.
		if strings.Contains(norm, query) || strings.Contains(query, norm) {
			candidates = append(candidates, candidate{name: n, dist: -1})
			continue
		}
		d := levenshtein(query, norm)
		// Dynamic threshold: allow more edits for longer queries.
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

	// Sort: substring matches (-1) first, then by distance.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].dist < candidates[i].dist {
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

// normalizeSkillName lowercases and strips non-alphanumeric characters.
// "browser_automation" and "browser-automation" both become "browserautomation".
func normalizeSkillName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// levenshtein and min3 are defined in file_suggest.go and tool_suggest.go respectively.
