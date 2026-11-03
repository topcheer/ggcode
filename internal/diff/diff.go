package diff

import (
	"fmt"
	"strings"
)

const maxDiffLines = 5000

func UnifiedDiff(old, new string, contextLines int) string {
	if old == new {
		return ""
	}

	oldLines, oldNoNL := splitLinesEOF(old)
	newLines, newNoNL := splitLinesEOF(new)

	if len(oldLines)+len(newLines) > maxDiffLines {
		return fastDiffFallback(oldLines, newLines, contextLines, oldNoNL, newNoNL)
	}

	editScript := computeEditScript(oldLines, newLines)

	if !scriptHasChanges(editScript) {
		if oldNoNL != newNoNL && len(oldLines) > 0 && len(newLines) > 0 {
			return formatEOFNewlineDiff(oldLines, newLines, oldNoNL, newNoNL)
		}
		return ""
	}

	return formatUnifiedDiff(oldLines, newLines, editScript, contextLines, oldNoNL, newNoNL)
}

func scriptHasChanges(script []op) bool {
	for _, s := range script {
		if s.kind == '+' || s.kind == '-' {
			return true
		}
	}
	return false
}

func formatEOFNewlineDiff(oldLines, newLines []string, oldNoNL, newNoNL bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("@@ -%d,1 +%d,1 @@\n", len(oldLines), len(newLines)))
	sb.WriteString("- " + oldLines[len(oldLines)-1] + "\n")
	if oldNoNL {
		sb.WriteString(noNewlineMarker + "\n")
	}
	sb.WriteString("+ " + newLines[len(newLines)-1] + "\n")
	if newNoNL {
		sb.WriteString(noNewlineMarker + "\n")
	}
	return sb.String()
}

func fastDiffFallback(oldLines, newLines []string, contextLines int, oldNoNL, newNoNL bool) string {

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

	if oldNoNL != newNoNL && len(oldLines) > 0 && len(newLines) > 0 &&
		prefix+suffix == len(oldLines) && prefix+suffix == len(newLines) {
		sb.WriteString("- " + oldLines[len(oldLines)-1] + "\n")
		if oldNoNL {
			sb.WriteString(noNewlineMarker + "\n")
		}
		sb.WriteString("+ " + newLines[len(newLines)-1] + "\n")
		if newNoNL {
			sb.WriteString(noNewlineMarker + "\n")
		}
	}

	return sb.String()
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")

	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

const noNewlineMarker = "\\ No newline at end of file"

func splitLinesEOF(text string) (lines []string, noNewline bool) {
	if text == "" {
		return nil, false
	}
	return splitLines(text), !strings.HasSuffix(text, "\n")
}

type op struct {
	kind byte
	text string
}

func computeEditScript(old, new []string) []op {
	m, n := len(old), len(new)

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

	for l, r := 0, len(script)-1; l < r; l, r = l+1, r-1 {
		script[l], script[r] = script[r], script[l]
	}

	return script
}

type lineEntry struct {
	kind    byte
	text    string
	oldNum  int
	newNum  int
	inGroup bool
}

func formatUnifiedDiff(oldLines, newLines []string, script []op, contextLines int, oldNoNL, newNoNL bool) string {

	oldLine, newLine := 1, 1
	var entries []lineEntry

	changePositions := make(map[int]bool)
	for idx, s := range script {
		if s.kind != '=' {
			changePositions[idx] = true
		}
	}

	inChange := false
	for idx, s := range script {
		isChange := changePositions[idx]

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

	var sb strings.Builder
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e.kind != '~' {
			sb.WriteString(fmt.Sprintf("%c %s\n", e.kind, e.text))

			oldEOF := oldNoNL && e.oldNum == len(oldLines) && (e.kind == '-' || e.kind == ' ')
			newEOF := newNoNL && e.newNum == len(newLines) && (e.kind == '+' || e.kind == ' ')
			if oldEOF || newEOF {
				sb.WriteString(noNewlineMarker + "\n")
			}
			continue
		}
		if header, ok := hunkHeader(entries, i); ok {
			sb.WriteString(header)
		}
	}

	return sb.String()
}

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

func HasChanges(old, new string) bool {
	return old != new
}

func CountChanges(old, new string) (additions, deletions int) {
	oldLines, oldNoNL := splitLinesEOF(old)
	newLines, newNoNL := splitLinesEOF(new)
	if len(oldLines)+len(newLines) > maxDiffLines {

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
		if isEOFNewlineOnlyChange(oldLines, newLines, oldNoNL, newNoNL) {
			return 1, 1
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

	if isEOFNewlineOnlyChange(oldLines, newLines, oldNoNL, newNoNL) {
		return 1, 1
	}
	return
}

func isEOFNewlineOnlyChange(oldLines, newLines []string, oldNoNL, newNoNL bool) bool {
	return oldNoNL != newNoNL && len(oldLines) > 0 && len(newLines) > 0 &&
		equalLines(oldLines, newLines)
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func Stats(old, new string) string {
	additions, deletions := CountChanges(old, new)
	return fmt.Sprintf("+%d additions, -%d deletions", additions, deletions)
}
