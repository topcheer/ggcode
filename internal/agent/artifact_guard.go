package agent

// Leftover artifact detection for written code.
//
// Research basis: AI coding agents frequently produce or carry over artifacts
// that should never appear in committed source code:
//
//  1. Merge conflict markers — when the conversation context, a patch, or a
//     diff snippet contains conflict markers, agents sometimes include them
//     verbatim in the written file. This is a guaranteed build failure in
//     every language (e.g., Go, Python, JS all choke on `<<<<<<<` in source).
//     Claude Code does not detect this; Cursor's diff review sometimes does;
//     OpenHands/Cline rely on the build cycle.
//
//  2. Content duplication / massive growth — agents occasionally double-paste
//     content or expand a file far beyond its original size due to edit_file
//     mis-matching or write_file duplicating blocks. A sudden 10x growth in
//     a single edit almost always indicates an error.
//
// ggcode's approach: detect these at write time with zero-LLM-cost,
// language-agnostic heuristics. Merge conflict markers have zero false
// positives (they are never legitimate source code). Massive growth uses a
// generous threshold (5x) to minimize false positives while still catching
// the most egregious duplication failures.

import (
	"fmt"
	"strings"
)

// mergeConflictMarkers are git conflict marker prefixes that never belong
// in committed source files.
var mergeConflictMarkers = []string{
	"<<<<<<<", // conflict start (e.g. "<<<<<<< HEAD")
	">>>>>>>", // conflict end   (e.g. ">>>>>>> branch-name")
	"=======", // conflict separator (7 equals, exact git marker)
}

// checkMergeConflictMarkers detects git merge conflict markers in the new
// content. Returns a warning string if markers are found, "" otherwise.
//
// We check only the new content (not a diff against old) because conflict
// markers are ALWAYS errors regardless of whether they existed before —
// if the old file already had them, that's a pre-existing bug that should
// still be flagged.
func checkMergeConflictMarkers(filePath, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	found := 0
	for _, marker := range mergeConflictMarkers {
		// Count lines that start with the marker (ignoring leading whitespace
		// to catch indented copies, but being careful with "=======" which
		// could appear in legitimate base64 or hex — so we require it to be
		// on its own line or followed by whitespace/newline).
		found += countConflictMarkerLines(newContent, marker)
	}

	if found == 0 {
		return ""
	}

	noun := "marker"
	if found > 1 {
		noun = "markers"
	}
	return fmt.Sprintf(
		"File contains %d git merge conflict %s (<<<<<<< ======= >>>>>>>). "+
			"These cause build failures in every language — resolve the conflict and remove ALL markers.",
		found, noun)
}

// countConflictMarkerLines counts lines that are merge conflict markers.
// For "<<<<<<<" and ">>>>>>>", a line counts if it starts with (after trimming
// leading whitespace) the marker followed by a space, newline, or end-of-line.
// For "=======", we require the trimmed line to BE exactly the marker to avoid
// false positives from base64/hex strings.
func countConflictMarkerLines(content, marker string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch marker {
		case "<<<<<<<", ">>>>>>>":
			if strings.HasPrefix(trimmed, marker) {
				rest := trimmed[len(marker):]
				// Must be followed by space, tab, or end-of-line to count.
				if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' {
					count++
				}
			}
		case "=======":
			// Require exact match to avoid false positives.
			if strings.TrimRight(trimmed, " \t\r") == marker {
				count++
			}
		}
	}
	return count
}

// checkContentGrowth detects when an edit causes the file to grow
// dramatically relative to its original size, which often indicates
// accidental content duplication.
//
// The threshold is deliberately generous (5x) to minimize false positives:
// legitimate additions like appending a new function or expanding a test
// suite rarely cause 5x growth. The real failures this catches are
// double-pastes and whole-file duplications.
//
// Edge cases handled:
//   - New files (oldContent empty) are skipped — growth ratio is undefined.
//   - Very small files (<10 lines old) are skipped — a 5x ratio is meaningless
//     for a 3-line file becoming 15 lines.
//   - Minimum old size (10 lines) ensures we only flag substantial duplication.
func checkContentGrowth(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(oldContent) == "" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldLines := strings.Count(oldContent, "\n") + 1
	newLines := strings.Count(newContent, "\n") + 1

	// Skip tiny files — ratio is meaningless.
	if oldLines < 10 {
		return ""
	}

	ratio := float64(newLines) / float64(oldLines)

	// 5x growth threshold: catches double-paste (2x+2x) and full-file
	// duplication while allowing legitimate large additions.
	if ratio >= 5.0 {
		return fmt.Sprintf(
			"File grew from %d to %d lines (%.1fx increase) in a single edit. "+
				"This often indicates accidental content duplication — verify the file content is correct.",
			oldLines, newLines, ratio)
	}

	return ""
}
