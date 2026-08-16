package diff

import (
	"fmt"
	"strings"
)

// maxDiffLines is the safety limit for LCS computation. Files exceeding this
// combined line count get a truncated diff instead of running the O(n*m)
// algorithm, which would freeze the TUI on very large files.
const maxDiffLines = 5000

// UnifiedDiff generates a unified diff string between old and new content.
// contextLines controls how many unchanged lines surround each change.
// For very large files (>5000 combined lines), falls back to showing only
// the first N changed lines to avoid OOM and TUI freeze.
func UnifiedDiff(old, new string, contextLines int) string {
	oldLines := splitLines(old)
	newLines := splitLines(new)

	// Safety: if the combined input is too large, use a line-based fallback
	// that avoids the O(n*m) LCS table allocation.
	if len(oldLines)+len(newLines) > maxDiffLines {
		return fastDiffFallback(oldLines, newLines, contextLines)
	}

	editScript := computeEditScript(oldLines, newLines)
	return formatUnifiedDiff(oldLines, newLines, editScript, contextLines)
}

// fastDiffFallback produces a diff for large files without the O(n*m) LCS
// table. It uses a simple prefix/suffix matching approach to find the common
// header and trailer, then shows only the changed middle section.
func fastDiffFallback(oldLines, newLines []string, contextLines int) string {
	// Find common prefix
	prefix := 0
	minLen := len(oldLines)
	if len(newLines) < minLen {
		minLen = len(newLines)
	}
	for prefix < minLen && oldLines[prefix] == newLines[prefix] {
		prefix++
	}

	// Find common suffix
	suffix := 0
	for suffix < (len(oldLines)-prefix) && suffix < (len(newLines)-prefix) &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("(diff truncated: %d+%d lines, showing changes only)\n", len(oldLines), len(newLines)))

	if prefix > 0 {
		show := contextLines
		if show > prefix {
			show = prefix
		}
		start := prefix - show
		for i := start; i < prefix; i++ {
			sb.WriteString("  " + oldLines[i] + "\n")
		}
	}

	for i := prefix; i < len(oldLines)-suffix; i++ {
		sb.WriteString("- " + oldLines[i] + "\n")
	}
	for i := prefix; i < len(newLines)-suffix; i++ {
		sb.WriteString("+ " + newLines[i] + "\n")
	}

	if suffix > 0 {
		show := contextLines
		if show > suffix {
			show = suffix
		}
		for i := 0; i < show; i++ {
			sb.WriteString("  " + oldLines[len(oldLines)-suffix+i] + "\n")
		}
	}

	return sb.String()
}

// splitLines splits text into lines, preserving a trailing newline indicator.
// Empty input yields no lines: an empty file is zero lines, not one empty
// line, so diffs/stats for file creation don't report a phantom deletion
// of an empty line (#537).
func splitLines(text string) []string {
	if text == "" {
		return nil
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

// lineEntry is one rendered line of a unified diff: a content line (kind
// ' ', '+', '-') with its old/new file line numbers, or a hunk marker
// (kind '~').
type lineEntry struct {
	kind    byte
	text    string
	oldNum  int
	newNum  int
	inGroup bool
}

// formatUnifiedDiff formats an edit script as unified diff.
func formatUnifiedDiff(oldLines, newLines []string, script []op, contextLines int) string {
	// First pass: mark which lines are in change groups
	oldLine, newLine := 1, 1
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
			if !inChange {
				// Hunk start marker. Also fires at idx 0 so a hunk beginning at
				// the first line still gets a header (#537).
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

	// Format output. Hunk markers get a standard unified diff header
	// "@@ -start,count +start,count @@" computed from the lines that follow
	// up to the next marker. Bare "@@" markers are not a valid unified diff
	// and left LLM/consumers without line-number anchors (#537).
	var sb strings.Builder
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e.kind != '~' {
			sb.WriteString(fmt.Sprintf("%c %s\n", e.kind, e.text))
			continue
		}
		if header, ok := hunkHeader(entries, i); ok {
			sb.WriteString(header)
		}
	}

	return sb.String()
}

// hunkHeader computes the standard unified diff header "@@ -l,s +l,s @@"
// for the hunk marker at index markerIdx. It scans the entries that follow
// the marker (up to the next marker or end), anchoring on the first old/new
// line numbers and counting context and change lines on each side. It
// returns ok=false for a stray marker with no following lines (adjacent
// markers around a one-line gap, or the trailing marker), which gets no
// header.
func hunkHeader(entries []lineEntry, markerIdx int) (string, bool) {
	oldStart, newStart, oldCount, newCount := 0, 0, 0, 0
	for j := markerIdx + 1; j < len(entries) && entries[j].kind != '~'; j++ {
		le := entries[j]
		if oldStart == 0 && le.oldNum > 0 {
			oldStart = le.oldNum
		}
		if newStart == 0 && le.newNum > 0 {
			newStart = le.newNum
		}
		if le.kind == '-' || le.kind == ' ' {
			oldCount++
		}
		if le.kind == '+' || le.kind == ' ' {
			newCount++
		}
	}
	if oldCount == 0 && newCount == 0 {
		return "", false
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount), true
}

// HasChanges returns true if old and new content differ.
func HasChanges(old, new string) bool {
	return old != new
}

// CountChanges returns the number of added and deleted lines.
func CountChanges(old, new string) (additions, deletions int) {
	oldLines := splitLines(old)
	newLines := splitLines(new)
	if len(oldLines)+len(newLines) > maxDiffLines {
		// For large files, count prefix/suffix matching
		prefix := 0
		minLen := len(oldLines)
		if len(newLines) < minLen {
			minLen = len(newLines)
		}
		for prefix < minLen && oldLines[prefix] == newLines[prefix] {
			prefix++
		}
		suffix := 0
		for suffix < (len(oldLines)-prefix) && suffix < (len(newLines)-prefix) &&
			oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
			suffix++
		}
		deletions = len(oldLines) - prefix - suffix
		additions = len(newLines) - prefix - suffix
		if deletions < 0 {
			deletions = 0
		}
		if additions < 0 {
			additions = 0
		}
		return
	}
	script := computeEditScript(oldLines, newLines)
	for _, s := range script {
		switch s.kind {
		case '+':
			additions++
		case '-':
			deletions++
		}
	}
	return
}

// Stats returns a human-readable summary of changes.
func Stats(old, new string) string {
	additions, deletions := CountChanges(old, new)
	return fmt.Sprintf("+%d additions, -%d deletions", additions, deletions)
}
