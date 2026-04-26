package diff

import (
	"fmt"
	"strings"
)

// UnifiedDiff generates a unified diff string between old and new content.
// contextLines controls how many unchanged lines surround each change.
func UnifiedDiff(old, new string, contextLines int) string {
	oldLines := splitLines(old)
	newLines := splitLines(new)

	editScript := computeEditScript(oldLines, newLines)
	return formatUnifiedDiff(oldLines, newLines, editScript, contextLines)
}

// splitLines splits text into lines, preserving a trailing newline indicator.
func splitLines(text string) []string {
	if text == "" {
		return []string{""}
	}
	lines := strings.Split(text, "\n")
	// Remove trailing empty string from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// op represents a line operation in the edit script.
type op struct {
	kind byte // ' ', '-', '+', '='
	text string
}

// computeEditScript computes a simple LCS-based edit script.
func computeEditScript(old, new []string) []op {
	m, n := len(old), len(new)

	// Build LCS table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to build edit script
	var script []op
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && old[i-1] == new[j-1] {
			script = append(script, op{'=', old[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			script = append(script, op{'+', new[j-1]})
			j--
		} else {
			script = append(script, op{'-', old[i-1]})
			i--
		}
	}

	// Reverse to get forward order
	for l, r := 0, len(script)-1; l < r; l, r = l+1, r-1 {
		script[l], script[r] = script[r], script[l]
	}

	return script
}

// formatUnifiedDiff formats an edit script as unified diff.
func formatUnifiedDiff(oldLines, newLines []string, script []op, contextLines int) string {
	// First pass: mark which lines are in change groups
	oldLine, newLine := 1, 1
	type lineEntry struct {
		kind    byte
		text    string
		oldNum  int
		newNum  int
		inGroup bool
	}
	var entries []lineEntry

	// Identify change positions
	changePositions := make(map[int]bool)
	for idx, s := range script {
		if s.kind != '=' {
			changePositions[idx] = true
		}
	}

	// Expand context around changes
	inChange := false
	for idx, s := range script {
		isChange := changePositions[idx]

		// Check if within context of any change
		nearChange := false
		if isChange {
			nearChange = true
		} else {
			for cp := range changePositions {
				dist := idx - cp
				if dist < 0 {
					dist = -dist
				}
				if dist <= contextLines {
					nearChange = true
					break
				}
			}
		}

		if nearChange {
			if !inChange && idx > 0 {
				// Truncation marker
				entries = append(entries, lineEntry{kind: '~', text: "@@"})
			}
			inChange = true
		} else {
			if inChange {
				entries = append(entries, lineEntry{kind: '~', text: "@@"})
				inChange = false
			}
			continue
		}

		kind := byte(' ')
		on, nn := 0, 0
		switch s.kind {
		case '=':
			kind = ' '
			on = oldLine
			nn = newLine
			oldLine++
			newLine++
		case '-':
			kind = '-'
			on = oldLine
			oldLine++
		case '+':
			kind = '+'
			nn = newLine
			newLine++
		}

		entries = append(entries, lineEntry{kind: kind, text: s.text, oldNum: on, newNum: nn, inGroup: nearChange})
	}

	if inChange {
		entries = append(entries, lineEntry{kind: '~', text: "@@"})
	}

	// Format output
	var sb strings.Builder
	for _, e := range entries {
		switch e.kind {
		case '~':
			sb.WriteString(fmt.Sprintf("%s\n", e.text))
		default:
			sb.WriteString(fmt.Sprintf("%c %s\n", e.kind, e.text))
		}
	}

	return sb.String()
}

// HasChanges returns true if old and new content differ.
func HasChanges(old, new string) bool {
	return old != new
}
