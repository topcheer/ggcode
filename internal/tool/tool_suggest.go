package tool

import (
	"fmt"
	"strings"
)

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

		isPrefix := strings.HasPrefix(cand, name) || strings.HasPrefix(name, cand)
		if isPrefix && len(name) >= 3 {

			if 1 < bestDist || (1 == bestDist && t.Name() < bestName) {
				bestDist = 1
				bestName = t.Name()
			}
			continue
		}

		if dist < bestDist || (dist == bestDist && t.Name() < bestName) {
			bestDist = dist
			bestName = t.Name()
		}
	}

	if bestDist > maxToolNameDistance {
		return ""
	}
	return bestName
}

const maxToolNameDistance = 3

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
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
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

func FormatUnknownToolError(registry *Registry, name string) string {
	suggestion := SuggestToolName(registry, name)
	if suggestion == "" {
		return fmt.Sprintf("unknown tool: %q. Use the tool registry to see available tools.", name)
	}
	return fmt.Sprintf("unknown tool: %q. Did you mean %q?", name, suggestion)
}
