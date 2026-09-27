package agent

import (
	"strings"
	"testing"
)

// r156 consolidation: the quality-score regression metric absorbed the
// former quality_regression detector (deleted in the same change). These
// tests pin the new metric's gating, median quorum, and advisory format.

func TestCollectRunRegressionMetrics_ScoreDrop(t *testing.T) {
	base := perfBaselineEntry{Score: 0.8, Iterations: 3}

	// 25%+ drop below a healthy baseline median: hit.
	if hits := collectRunRegressionMetrics(perfBaselineEntry{Score: 0.5}, base); len(hits) != 1 || hits[0] != "quality_score" {
		t.Fatalf("expected quality_score hit, got %v", hits)
	}
	// Mild dip (0.7 > 0.8*0.75): no hit.
	if hits := collectRunRegressionMetrics(perfBaselineEntry{Score: 0.7}, base); len(hits) != 0 {
		t.Fatalf("mild dip should not regress, got %v", hits)
	}
	// Unscored current run (cancelled): never a regression.
	if hits := collectRunRegressionMetrics(perfBaselineEntry{Score: 0}, base); len(hits) != 0 {
		t.Fatalf("unscored run must not regress, got %v", hits)
	}
	// Weak baseline (below floor): silent.
	weak := perfBaselineEntry{Score: 0.3}
	if hits := collectRunRegressionMetrics(perfBaselineEntry{Score: 0.1}, weak); len(hits) != 0 {
		t.Fatalf("weak baseline must stay silent, got %v", hits)
	}
}

func TestComputeMedianBaseline_ScoreQuorum(t *testing.T) {
	// 6 successful runs, 5 scored: median over the scored subset only.
	runs := []perfBaselineEntry{
		{Success: true, Score: 0.9}, {Success: true, Score: 0.8},
		{Success: true, Score: 0.7}, {Success: true, Score: 0.6},
		{Success: true, Score: 0.5}, {Success: true},
	}
	mid := computeMedianBaseline(runs)
	if mid.Score != 0.7 {
		t.Fatalf("median score = %v, want 0.7", mid.Score)
	}

	// Too few scored entries (quorum not met): scoreless baseline.
	runs = append(runs[:3], runs[4], perfBaselineEntry{Success: true}) // 2 scored
	if mid := computeMedianBaseline(runs); mid.Score != 0 {
		t.Fatalf("scoreless quorum should yield 0, got %v", mid.Score)
	}

	// Legacy all-zero baselines stay scoreless.
	legacy := make([]perfBaselineEntry, 6)
	for i := range legacy {
		legacy[i] = perfBaselineEntry{Success: true, Iterations: 4}
	}
	if mid := computeMedianBaseline(legacy); mid.Score != 0 {
		t.Fatalf("legacy baseline should be scoreless, got %v", mid.Score)
	}
}

func TestPerfMetricOrder_QualityScoreLast(t *testing.T) {
	// quality_score must stay lowest-priority: appending it must not change
	// existing metric precedence in collectRunRegressionMetrics/pickConsensus.
	if got := perfMetricOrder[len(perfMetricOrder)-1]; got != "quality_score" {
		t.Fatalf("quality_score should be last in metric order, order=%v", perfMetricOrder)
	}
}

func TestFormatPerfRegressionWarning_QualityScore(t *testing.T) {
	base := perfBaselineEntry{Score: 0.8}
	latest := perfBaselineEntry{Score: 0.5}
	msg := formatPerfRegressionWarning("quality_score", base, latest)
	if !strings.Contains(msg, "[Performance regression]") {
		t.Fatalf("missing regression header: %q", msg)
	}
	if !strings.Contains(msg, "quality score") {
		t.Fatalf("missing metric name: %q", msg)
	}
	if !strings.Contains(msg, "baseline=80") || !strings.Contains(msg, "recent=50") {
		t.Fatalf("missing score percentages: %q", msg)
	}
}

func TestPerfMetricValue_QualityScore(t *testing.T) {
	if v := perfMetricValue(perfBaselineEntry{Score: 0.512}, "quality_score"); v != 512 {
		t.Fatalf("scaled value = %d, want 512", v)
	}
}
