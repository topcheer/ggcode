package tool

// Merge Conflict Marker Detection for files being read or edited.
//
// Research basis: Claude Code highlights conflict markers when viewing files;
// Cursor shows inline conflict resolution UI; GitHub's web editor warns about
// unresolved conflicts. Without detection at read time, the AI agent can:
//   - Read a file with conflict markers and attempt edits on malformed content
//   - Unwittingly remove or mangle conflict markers during edits
//   - Waste iterations on builds/tests that fail due to unresolved conflicts
//   - Commit files that still contain conflict markers
//
// ggcode already detects conflict markers at commit time (diff_scan.go), but
// this is too late — the agent may have already made edits on top of the
// conflicts. This module provides early detection at read time, giving the
// agent immediate awareness so it can resolve conflicts before proceeding.
//
// Design: zero-LLM-cost, non-blocking. A concise warning is appended to the
// tool result. The agent sees the conflict regions and can act accordingly.

import (
	"fmt"
	"strings"
)

// conflictMarkerStart matches the beginning of a conflict region: "<<<<<<< HEAD",
// "<<<<<<< feature-branch", etc. Git always uses 7 angle brackets.
const conflictMarkerStart = "<<<<<<<"

// conflictMarkerMid matches the separator line: "=======" (7 equals).
const conflictMarkerMid = "======="

// conflictMarkerEnd matches the end of a conflict region: ">>>>>>> branch-name".
const conflictMarkerEnd = ">>>>>>>"

// ConflictRegion represents a single unresolved merge conflict in a file.
type ConflictRegion struct {
	StartLine int    // line number (1-based) of the "<<<<<<<" marker
	MidLine   int    // line number of the "=======" separator
	EndLine   int    // line number of the ">>>>>>>" marker
	Branch1   string // label after <<<<<<< (e.g. "HEAD")
	Branch2   string // label after >>>>>>> (e.g. "feature/foo")
}

// maxConflictRegions caps the number of conflict regions reported to avoid
// flooding the agent's context in pathological cases.
const maxConflictRegions = 5

// DetectMergeConflicts scans file content for git merge conflict markers and
// returns the conflict regions found. Returns nil if no conflicts are detected.
//
// The detection is line-based and matches the standard git conflict format:
//
//	<<<<<<< HEAD
//	  ... our changes ...
//	=======
//	  ... their changes ...
//	>>>>>>> feature/branch
//
// Also supports the less common "diff3" style with a "|||||||" base marker.
func DetectMergeConflicts(content string) []ConflictRegion {
	lines := strings.Split(content, "\n")
	var regions []ConflictRegion

	var current *ConflictRegion
	lineNum := 0

	for _, line := range lines {
		lineNum++
		trimmed := strings.TrimRight(line, "\r")

		if strings.HasPrefix(trimmed, conflictMarkerStart) {
			// Start of a new conflict region
			if current != nil {
				// Previous conflict was not properly closed — record it as-is
				regions = appendConflictRegion(regions, *current)
			}
			current = &ConflictRegion{
				StartLine: lineNum,
				Branch1:   strings.TrimSpace(strings.TrimPrefix(trimmed, conflictMarkerStart)),
			}
		} else if current != nil && strings.HasPrefix(trimmed, conflictMarkerMid) && trimmed == conflictMarkerMid {
			// Separator line (must be exactly 7 equals, not part of code)
			if current.MidLine == 0 {
				current.MidLine = lineNum
			}
		} else if current != nil && strings.HasPrefix(trimmed, conflictMarkerEnd) {
			// End of conflict region
			current.EndLine = lineNum
			current.Branch2 = strings.TrimSpace(strings.TrimPrefix(trimmed, conflictMarkerEnd))
			regions = appendConflictRegion(regions, *current)
			current = nil
		}
	}

	// Handle unclosed conflict region
	if current != nil {
		regions = appendConflictRegion(regions, *current)
	}

	return regions
}

// appendConflictRegion appends a region to the slice, respecting the max cap.
func appendConflictRegion(regions []ConflictRegion, r ConflictRegion) []ConflictRegion {
	if len(regions) >= maxConflictRegions {
		return regions
	}
	return append(regions, r)
}

// FormatConflictWarning builds a concise warning message for the agent when
// merge conflict markers are detected in a file. Returns empty string if no
// conflicts are provided.
func FormatConflictWarning(regions []ConflictRegion) string {
	if len(regions) == 0 {
		return ""
	}

	n := len(regions)
	var sb strings.Builder

	if n == 1 {
		r := regions[0]
		sb.WriteString(fmt.Sprintf(
			"\n\n[WARNING] This file contains an unresolved merge conflict (lines %d-%d). "+
				"Resolve the conflict before editing — editing across conflict markers can corrupt the file. "+
				"Conflict between: %s vs %s.",
			r.StartLine, r.EndLine,
			conflictLabel(r.Branch1), conflictLabel(r.Branch2),
		))
	} else {
		sb.WriteString(fmt.Sprintf(
			"\n\n[WARNING] This file contains %d unresolved merge conflicts. "+
				"Resolve all conflicts before editing — editing across conflict markers can corrupt the file.",
			n,
		))
		for _, r := range regions {
			sb.WriteString(fmt.Sprintf(
				"\n  - Lines %d-%d: %s vs %s",
				r.StartLine, r.EndLine,
				conflictLabel(r.Branch1), conflictLabel(r.Branch2),
			))
		}
	}

	return sb.String()
}

// conflictLabel returns a human-readable label for a conflict branch.
func conflictLabel(label string) string {
	if label == "" {
		return "(unknown)"
	}
	return label
}

// CheckContentForConflicts is a convenience function that detects conflicts in
// content and returns a formatted warning string. Returns empty string if no
// conflicts are found. Used by read_file and edit_file tools.
func CheckContentForConflicts(content string) string {
	regions := DetectMergeConflicts(content)
	return FormatConflictWarning(regions)
}
