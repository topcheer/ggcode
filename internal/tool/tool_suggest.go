package tool

import (
	"fmt"
	"strings"
)

// SuggestToolName returns a suggestion for an unknown tool name by finding the
// closest registered tool name by Levenshtein distance. Returns "" if no close
// match exists (distance > maxAllowedDistance or no tools registered).
//
// Weak models frequently misspell or slightly misremember tool names —
// "readfile" instead of "read_file", "edit" instead of "edit_file", "grep_files"
// instead of "grep". Without a suggestion, the agent wastes a full loop
// iteration on a bare "unknown tool" error before correcting itself.
func SuggestToolName(registry *Registry, name string) string {
	tools := registry.List()
	if len(tools) == 0 || name == "" {
		return ""
	}

	name = strings.ToLower(strings.TrimSpace(name))
	bestName := ""
	bestDist := maxToolNameDistance + 1

	for _, t := range tools {
		cand := strings.ToLower(t.Name())
		dist := toolNameDistance(name, cand)

		// Also check prefix match — "edit" is likely "edit_file" even if
		// the edit distance is moderate.
		isPrefix := strings.HasPrefix(cand, name) || strings.HasPrefix(name, cand)
		if isPrefix && len(name) >= 3 {
			// Prefix match is a strong signal; treat as distance 1.
			if 1 < bestDist {
				bestDist = 1
				bestName = t.Name()
			}
			continue
		}

		if dist < bestDist {
			bestDist = dist
			bestName = t.Name()
		}
	}

	if bestDist > maxToolNameDistance {
		return ""
	}
	return bestName
}

// maxToolNameDistance is the maximum Levenshtein distance for a suggestion.
// Tuned so that common misspellings (readfile→read_file=1, editfile→edit_file=1,
// grepp→grep=1) are caught, but unrelated names are not.
const maxToolNameDistance = 3

// toolNameDistance computes the Levenshtein edit distance between two
// lowercased tool names. Uses the standard dynamic-programming algorithm with
// O(len(a)*len(b)) time and O(min(len(a),len(b))) space.
func toolNameDistance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Ensure a is the shorter string to minimize space.
	if len(b) < len(a) {
		a, b = b, a
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// FormatUnknownToolError builds an actionable error message for an unknown
// tool name, including a "Did you mean ...?" suggestion when one exists.
func FormatUnknownToolError(registry *Registry, name string) string {
	suggestion := SuggestToolName(registry, name)
	if suggestion == "" {
		return fmt.Sprintf("unknown tool: %q. Use the tool registry to see available tools.", name)
	}
	return fmt.Sprintf("unknown tool: %q. Did you mean %q?", name, suggestion)
}
