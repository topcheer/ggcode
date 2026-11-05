package agent

import (
	"path/filepath"
	"testing"
)

// TestIssue5038QualifyingModelWithoutTagNoPanic pins the original #5038
// concern (a qualifying sibling model that never recorded the probed tag)
// under the #5088 absence-as-signal gate: glm-5.2 (current) considered
// "Multi File Edit" (present, never delivered) so it is NOT stale, while
// "Context Decay" - delivered 3x by the tagless-for-MFE sibling glm-5.3 -
// is absent from glm-5.2 entirely and IS reported. No panic.
func TestIssue5038QualifyingModelWithoutTagNoPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-11-04T10:00:"+pad(i), "glm-5.2", "Multi File Edit", 0),
			statLine("2026-11-04T11:00:"+pad(i), "glm-5.3", "Context Decay", 3),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	got := staleHeuristicSummary(path, "")
	if len(got) != 1 || got[0] != "Context Decay" {
		t.Fatalf("want stale report only for absent Context Decay, got %v", got)
	}
}

// TestIssue5038MixedOtherModels verifies a tagless sibling does not mask a
// real cross-model contrast: glm-5.4 delivers "Multi File Edit" above the
// bar while glm-5.3 never records it; glm-5.2 never considered the tag at
// all and recorded "Context Decay" only as suppressed (budget-starved, not
// stale), so exactly Multi File Edit is reported.
func TestIssue5038MixedOtherModelsStillReports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLineSuppressed("2026-11-04T09:30:"+pad(i), "glm-5.2", "Context Decay", 0, 1),
			statLine("2026-11-04T10:00:"+pad(i), "glm-5.2", "Filler Guidance", 1),
			statLine("2026-11-04T11:00:"+pad(i), "glm-5.3", "Context Decay", 3),
			statLine("2026-11-04T12:00:"+pad(i), "glm-5.4", "Multi File Edit", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	got := staleHeuristicSummary(path, "glm-5.2")
	if len(got) != 1 || got[0] != "Multi File Edit" {
		t.Fatalf("want stale report for Multi File Edit on glm-5.2 despite a tagless sibling model, got %v", got)
	}
}
