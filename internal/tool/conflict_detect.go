package tool

import (
	"fmt"
	"strings"
)

const conflictMarkerStart = "<<<<<<<"

const conflictMarkerMid = "======="

const conflictMarkerEnd = ">>>>>>>"

type ConflictRegion struct {
	StartLine int
	MidLine   int
	EndLine   int
	Branch1   string
	Branch2   string
	Unclosed  bool
}

const maxConflictRegions = 5

func DetectMergeConflicts(content string) []ConflictRegion {
	lines := strings.Split(content, "\n")
	var regions []ConflictRegion

	var current *ConflictRegion
	lineNum := 0

	for _, line := range lines {
		lineNum++
		trimmed := strings.TrimRight(line, "\r")

		if strings.HasPrefix(trimmed, conflictMarkerStart) &&
			(len(trimmed) == len(conflictMarkerStart) || trimmed[len(conflictMarkerStart)] == ' ') {

			if current != nil {

				current.Unclosed = true
				regions = appendConflictRegion(regions, *current)
			}
			current = &ConflictRegion{
				StartLine: lineNum,
				Branch1:   strings.TrimSpace(strings.TrimPrefix(trimmed, conflictMarkerStart)),
			}
		} else if current != nil && strings.HasPrefix(trimmed, conflictMarkerMid) && trimmed == conflictMarkerMid {

			if current.MidLine == 0 {
				current.MidLine = lineNum
			}
		} else if current != nil && strings.HasPrefix(trimmed, conflictMarkerEnd) {

			current.EndLine = lineNum
			current.Branch2 = strings.TrimSpace(strings.TrimPrefix(trimmed, conflictMarkerEnd))
			regions = appendConflictRegion(regions, *current)
			current = nil
		}
	}

	if current != nil {

		current.Unclosed = true
		regions = appendConflictRegion(regions, *current)
	}

	return regions
}

func appendConflictRegion(regions []ConflictRegion, r ConflictRegion) []ConflictRegion {
	if len(regions) >= maxConflictRegions {
		return regions
	}
	return append(regions, r)
}

func FormatConflictWarning(regions []ConflictRegion) string {
	if len(regions) == 0 {
		return ""
	}

	n := len(regions)
	var sb strings.Builder

	if n == 1 {
		r := regions[0]

		rangeText := fmt.Sprintf("lines %d-%d", r.StartLine, r.EndLine)
		if r.Unclosed || r.EndLine == 0 {
			rangeText = fmt.Sprintf("line %d (unclosed conflict marker)", r.StartLine)
		}
		sb.WriteString(fmt.Sprintf(
			"\n\n[WARNING] This file contains an unresolved merge conflict (%s). "+
				"Resolve the conflict before editing — editing across conflict markers can corrupt the file. "+
				"Conflict between: %s vs %s.",
			rangeText,
			conflictLabel(r.Branch1), conflictLabel(r.Branch2),
		))
	} else {

		countWord := fmt.Sprintf("%d", n)
		if n >= maxConflictRegions {
			countWord = fmt.Sprintf("at least %d (list truncated)", n)
		}
		sb.WriteString(fmt.Sprintf(
			"\n\n[WARNING] This file contains %s unresolved merge conflicts. "+
				"Resolve all conflicts before editing — editing across conflict markers can corrupt the file.",
			countWord,
		))
		for _, r := range regions {

			rangeText := fmt.Sprintf("Lines %d-%d", r.StartLine, r.EndLine)
			if r.Unclosed || r.EndLine == 0 {
				rangeText = fmt.Sprintf("Line %d (unclosed conflict marker)", r.StartLine)
			}
			sb.WriteString(fmt.Sprintf(
				"\n  - %s: %s vs %s",
				rangeText,
				conflictLabel(r.Branch1), conflictLabel(r.Branch2),
			))
		}
	}

	return sb.String()
}

func conflictLabel(label string) string {
	if label == "" {
		return "(unknown)"
	}
	return label
}

func CheckContentForConflicts(content string) string {
	regions := DetectMergeConflicts(content)
	return FormatConflictWarning(regions)
}
