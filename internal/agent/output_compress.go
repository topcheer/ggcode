package agent

import (
	"strconv"
	"strings"
)

// Consecutive-line compression for tool output.
//
// Research basis: Context engineering studies (Anthropic 2025, Fundesk 2026)
// identify tool output as the dominant context consumer. Build logs, test
// output, and installation traces frequently contain long runs of identical
// or near-identical lines (e.g., "go: downloading ...", "--- PASS: TestX",
// "Compiling ..."). These runs waste context tokens without adding information.
//
// This pass runs BEFORE the size-based output guard, so compressed output
// may avoid truncation entirely. It is purely mechanical (no LLM cost) and
// preserves all unique information — only redundancy is collapsed.
//
// Two layers:
//  1. Exact consecutive duplicates: 3+ identical lines → 1 line + count marker
//  2. Prefix-similar consecutive lines: 5+ lines sharing a long common prefix
//     (10+ chars) → first 2 + last 1 + count marker

const (
	// exactDupThreshold is the minimum run length for exact duplicate compression.
	exactDupThreshold = 3

	// prefixSimilarThreshold is the minimum run length for prefix-similar compression.
	prefixSimilarThreshold = 5

	// minPrefixLen is the minimum common prefix length for two lines to be
	// considered "similar". Short prefixes would over-match and collapse
	// genuinely different lines.
	minPrefixLen = 10

	// maxCompressInputLines caps the number of lines processed to bound CPU.
	// Outputs beyond this are handled by the size-based output guard instead.
	maxCompressInputLines = 5000
)

// compressRepetitiveLines collapses consecutive identical or prefix-similar
// lines in tool output. Returns the compressed string. If the content has no
// compressible runs, it is returned unchanged.
func compressRepetitiveLines(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < exactDupThreshold {
		return content
	}
	if len(lines) > maxCompressInputLines {
		return content
	}

	var out []string
	i := 0
	for i < len(lines) {
		// Layer 1: exact consecutive duplicates.
		runEnd := i + 1
		for runEnd < len(lines) && lines[runEnd] == lines[i] {
			runEnd++
		}
		runLen := runEnd - i
		if runLen >= exactDupThreshold {
			out = append(out, lines[i])
			out = append(out, formatDupMarker(runLen-1, lines[i]))
			i = runEnd
			continue
		}

		// Layer 2: prefix-similar consecutive lines.
		// Two lines are "similar" if they share a common prefix of at least
		// minPrefixLen characters (or the entire shorter line if it's shorter).
		if len(lines[i]) >= minPrefixLen {
			prefix := lines[i][:minPrefixLen]
			sRunEnd := i + 1
			for sRunEnd < len(lines) && strings.HasPrefix(lines[sRunEnd], prefix) {
				sRunEnd++
			}
			sRunLen := sRunEnd - i
			if sRunLen >= prefixSimilarThreshold {
				// Keep first 2, last 1, and a count marker.
				out = append(out, lines[i])
				if i+1 < sRunEnd {
					out = append(out, lines[i+1])
				}
				if sRunEnd-1 > i+1 {
					out = append(out, lines[sRunEnd-1])
				}
				omitted := sRunLen - 3
				if omitted > 0 {
					out = append(out, formatSimilarMarker(omitted))
				}
				i = sRunEnd
				continue
			}
		}

		out = append(out, lines[i])
		i++
	}

	if len(out) == len(lines) {
		return content
	}
	return strings.Join(out, "\n")
}

// formatDupMarker generates a marker for omitted identical lines.
func formatDupMarker(omitted int, sample string) string {
	display := sample
	if len(display) > 60 {
		display = display[:57] + "..."
	}
	if strings.TrimSpace(display) == "" {
		return "[" + strconv.Itoa(omitted) + " identical blank lines omitted]"
	}
	return "[" + strconv.Itoa(omitted) + " identical lines omitted: " + display + "]"
}

// formatSimilarMarker generates a marker for omitted prefix-similar lines.
func formatSimilarMarker(omitted int) string {
	return "[" + strconv.Itoa(omitted) + " similar lines omitted]"
}
