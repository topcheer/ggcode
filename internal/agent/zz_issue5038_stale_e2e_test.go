package agent

import (
	"path/filepath"
	"testing"
)

// TestIssue5038 pins the nil-deref in analyzeStaleGuidance's others loop: a
// qualifying model (>= staleMinRuns runs) that never recorded the tag being
// probed yields tags[tag] == nil, and the pre-fix code dereferenced it for
// the delivered comparison. Scenario: glm-5.2 (current) has 10 zero-delivery
// runs of "Multi File Edit"; glm-5.3 also has 10 runs but only ever recorded
// a different tag, so deliveredPerModel["glm-5.3"]["Multi File Edit"] is nil.
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

	if s := staleHeuristicSummary(path, ""); len(s) != 0 {
		t.Fatalf("tag never delivered by any model must not be reported, got %v", s)
	}
}

// TestIssue5038MixedOtherModels verifies the nil guard does not mask a real
// cross-model contrast: with two qualifying other models, one recording the
// tag and one never recording it, the probe must skip the nil entry and still
// report from the recording model.
func TestIssue5038MixedOtherModelsStillReports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-11-04T10:00:"+pad(i), "glm-5.2", "Multi File Edit", 0),
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
