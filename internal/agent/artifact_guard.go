package agent

import (
	"fmt"
	"strings"
)

var mergeConflictMarkers = []string{
	"<<<<<<<",
	">>>>>>>",
	"=======",
}

func checkMergeConflictMarkers(filePath, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	found := 0
	for _, marker := range []string{"<<<<<<<", ">>>>>>>"} {

		found += countConflictMarkerLines(newContent, marker)
	}
	if found == 0 {

		return ""
	}

	found += countSeparatorsInsideConflictBlocks(newContent)

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

func countConflictMarkerLines(content, marker string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch marker {
		case "<<<<<<<", ">>>>>>>":
			if strings.HasPrefix(trimmed, marker) {
				rest := trimmed[len(marker):]

				if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' {
					count++
				}
			}
		case "=======":

			if strings.TrimRight(trimmed, " \t\r") == marker {
				count++
			}
		}
	}
	return count
}

func countSeparatorsInsideConflictBlocks(content string) int {
	count := 0
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "<<<<<<<") {
			rest := trimmed[len("<<<<<<<"):]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' {
				inBlock = true
				continue
			}
		}
		if strings.HasPrefix(trimmed, ">>>>>>>") {
			rest := trimmed[len(">>>>>>>"):]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' {
				inBlock = false
				continue
			}
		}
		if inBlock && strings.TrimRight(trimmed, " \t\r") == "=======" {
			count++
		}
	}
	return count
}

func checkContentGrowth(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(oldContent) == "" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldLines := countNonEmptyLines(oldContent)
	newLines := countNonEmptyLines(newContent)

	if oldLines < 10 {
		return ""
	}

	ratio := float64(newLines) / float64(oldLines)

	if ratio >= 5.0 {
		return fmt.Sprintf(
			"File grew from %d to %d lines (%.1fx increase) in a single edit. "+
				"This often indicates accidental content duplication — verify the file content is correct.",
			oldLines, newLines, ratio)
	}

	return ""
}

func countNonEmptyLines(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
