package agent

import (
	"path/filepath"
	"testing"
)

// #5088: the old stale gate required a (0,0) aggregate line (Delivered==0 &&
// Suppressed==0), but guidanceRunStats.record increments exactly one of the
// two counters per call and flushGuidanceStats only writes tags with >=1
// increment - so production data can never contain a (0,0) line and the
// feature could never fire. These tests pin the redesigned absence-as-signal
// gate using ONLY production-writable stat lines (every line has
// delivered>=1 or suppressed>=1 per tag).

// TestIssue5088AbsenceAsSignalFires: the current model runs >= staleMinRuns
// without ever considering the tag (no stat line at all) while a qualifying
// sibling delivers it above the bar - the report must fire.
func TestIssue5088AbsenceAsSignalFires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-11-05T10:00:"+pad(i), "glm-5.2", "Filler Guidance", 1),
			statLine("2026-11-05T11:00:"+pad(i), "glm-5.3", "Attention Fragmentation", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	got := staleHeuristicSummary(path, "glm-5.2")
	if len(got) != 1 || got[0] != "Attention Fragmentation" {
		t.Fatalf("absent tag delivered by sibling must be reported stale, got %v", got)
	}
}

// TestIssue5088SuppressedOnlyIsStarvedNotStale: a tag the current model
// considered but always suppressed is budget-starved (pinned semantics,
// production-writable shape), and must NOT be reported even with a sibling
// delivering above the bar.
func TestIssue5088SuppressedOnlyIsStarvedNotStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLineSuppressed("2026-11-05T10:00:"+pad(i), "glm-5.2", "Attention Fragmentation", 0, 1),
			statLine("2026-11-05T11:00:"+pad(i), "glm-5.3", "Attention Fragmentation", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	if got := staleHeuristicSummary(path, "glm-5.2"); len(got) != 0 {
		t.Fatalf("suppressed-only (budget-starved) tag must not be reported, got %v", got)
	}
}

// TestIssue5088SyntheticZeroZeroLineNotReported: even a synthetic (0,0) line
// (which production cannot write) means the tag WAS considered by the model;
// the absence gate must not report it either.
func TestIssue5088SyntheticZeroZeroLineNotReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-11-05T10:00:"+pad(i), "glm-5.2", "Attention Fragmentation", 0),
			statLine("2026-11-05T11:00:"+pad(i), "glm-5.3", "Attention Fragmentation", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	if got := staleHeuristicSummary(path, "glm-5.2"); len(got) != 0 {
		t.Fatalf("present tag (any record) must not be reported by the absence gate, got %v", got)
	}
}

// TestIssue5088CurrentModelUnderMinRunsNoReport: absence only counts once the
// current model itself has >= staleMinRuns recorded runs.
func TestIssue5088CurrentModelUnderMinRunsNoReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-11-05T11:00:"+pad(i), "glm-5.3", "Attention Fragmentation", 1),
		)
	}
	for i := 0; i < staleMinRuns-1; i++ {
		lines = append(lines,
			statLine("2026-11-05T10:00:"+pad(i), "glm-5.2", "Filler Guidance", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	if got := staleHeuristicSummary(path, "glm-5.2"); len(got) != 0 {
		t.Fatalf("current model under min runs must not be analyzed, got %v", got)
	}
}
