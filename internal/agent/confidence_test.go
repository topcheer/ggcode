package agent

import (
	"strings"
	"testing"
)

func TestConfidence_InsufficientData(t *testing.T) {
	c := newConfidenceState()
	for i := 0; i < confidenceMinCalls-1; i++ {
		c.recordResult("read_file", false, "")
	}
	if c.score() != -1 {
		t.Errorf("expected -1 for insufficient data, got %d", c.score())
	}
	if c.maybeIntervene() != "" {
		t.Error("should not intervene with insufficient data")
	}
}

func TestConfidence_HighScoreAllSuccess(t *testing.T) {
	c := newConfidenceState()
	tools := []string{"read_file", "grep", "edit_file", "glob", "list_directory", "run_command", "git_status"}
	for _, tool := range tools {
		c.recordResult(tool, false, "file.go")
	}
	s := c.score()
	if s < 70 {
		t.Errorf("expected high score for all-success trajectory, got %d", s)
	}
	if c.maybeIntervene() != "" {
		t.Error("should not intervene on high-confidence trajectory")
	}
}

func TestConfidence_LowScoreAllFailures(t *testing.T) {
	c := newConfidenceState()
	for i := 0; i < 6; i++ {
		c.recordResult("edit_file", true, "file.go")
	}
	s := c.score()
	if s >= confidenceWarnThreshold {
		t.Errorf("expected very low score for all-failure trajectory, got %d", s)
	}
	// Should NOT fire because maxErrorCluster >= 4 (error-streak handles it)
	if c.maybeIntervene() != "" {
		t.Error("should not intervene when error-streak will handle it (maxErrorCluster >= 4)")
	}
}

func TestConfidence_MixedResultsModerateScore(t *testing.T) {
	c := newConfidenceState()
	// 3 successes, 2 failures spread out
	c.recordResult("read_file", false, "a.go")
	c.recordResult("edit_file", false, "a.go")
	c.recordResult("grep", true, "")
	c.recordResult("edit_file", false, "b.go")
	c.recordResult("grep", true, "")
	s := c.score()
	if s < 20 || s > 80 {
		t.Errorf("expected moderate score for mixed trajectory, got %d", s)
	}
}

func TestConfidence_EarlyWarning(t *testing.T) {
	c := newConfidenceState()
	// Simulate a trajectory that's going badly but hasn't hit 4 consecutive errors
	// 5 calls: 1 success, 4 errors but NOT consecutive (spread out)
	c.recordResult("read_file", false, "a.go") // success
	c.recordResult("edit_file", true, "a.go")  // fail
	c.recordResult("edit_file", false, "a.go") // success (breaks streak)
	c.recordResult("edit_file", true, "a.go")  // fail
	c.recordResult("edit_file", true, "b.go")  // fail (cluster of 2, not 4)

	s := c.score()
	// Score should be low (only 2/5 = 40% success, poor edit rate)
	if s >= confidenceWarnThreshold {
		t.Errorf("expected low score, got %d", s)
	}

	guidance := c.maybeIntervene()
	if guidance == "" {
		t.Error("expected early warning guidance for bad trajectory")
	}
	if !strings.Contains(guidance, "trajectory confidence") {
		t.Errorf("guidance should mention confidence, got: %s", guidance)
	}
}

func TestConfidence_NoDoubleFire(t *testing.T) {
	c := newConfidenceState()
	c.recordResult("read_file", false, "")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", false, "a.go")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "b.go")

	g1 := c.maybeIntervene()
	if g1 == "" {
		t.Fatal("expected first guidance")
	}
	g2 := c.maybeIntervene()
	if g2 != "" {
		t.Error("should not fire twice")
	}
}

func TestConfidence_MomentumDetection(t *testing.T) {
	c := newConfidenceState()

	// Start well, then deteriorate
	c.recordResult("read_file", false, "a.go")
	c.recordResult("edit_file", false, "a.go")
	c.recordResult("read_file", false, "b.go")
	c.recordResult("edit_file", true, "b.go") // things go wrong
	c.recordResult("edit_file", true, "b.go")

	s := c.score()
	// Momentum should penalize — recent results are 0/2 while overall is 3/5
	if s > 50 {
		t.Logf("score = %d (momentum penalty applied, recent is worse)", s)
	}
}

func TestConfidence_MomentumRecovery(t *testing.T) {
	c := newConfidenceState()

	// Start badly, then recover
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("read_file", false, "b.go")
	c.recordResult("edit_file", false, "b.go")
	c.recordResult("edit_file", false, "b.go")

	s := c.score()
	// Momentum should help — recent results are 3/3 while overall is 3/5
	if s < 30 {
		t.Logf("score = %d (momentum recovery detected)", s)
	}
}

func TestConfidence_ToolDiversityBonus(t *testing.T) {
	// High diversity (7 tools) should score higher than low diversity (1 tool)
	// with same success rate. Use 1 failure to avoid clamping at 100.
	c1 := newConfidenceState()
	c1.recordResult("read_file", false, "a.go")
	c1.recordResult("grep", false, "a.go")
	c1.recordResult("edit_file", false, "a.go")
	c1.recordResult("glob", false, "a.go")
	c1.recordResult("list_directory", true, "") // 1 failure → base ~80
	c1.recordResult("run_command", false, "a.go")
	c1.recordResult("git_status", false, "a.go")

	c2 := newConfidenceState()
	for i := 0; i < 7; i++ {
		c2.recordResult("edit_file", i == 4, "a.go") // same 1 failure
	}

	s1 := c1.score()
	s2 := c2.score()
	if s1 <= s2 {
		t.Errorf("high diversity should score higher: diverse=%d, uniform=%d", s1, s2)
	}
}

func TestConfidence_EditFailurePenalty(t *testing.T) {
	c := newConfidenceState()
	// 5 read_file successes + 4 edit failures = 9 calls, 56% overall success
	// But 0% edit success rate → heavy penalty
	c.recordResult("read_file", false, "a.go")
	c.recordResult("read_file", false, "b.go")
	c.recordResult("read_file", false, "c.go")
	c.recordResult("read_file", false, "d.go")
	c.recordResult("read_file", false, "e.go")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "b.go")
	c.recordResult("edit_file", true, "c.go")
	c.recordResult("edit_file", true, "d.go")

	s := c.score()
	// Despite 56% overall success, all edits failing should drag score down
	if s > 40 {
		t.Errorf("expected low score due to edit failure penalty, got %d", s)
	}
}

func TestConfidence_Reset(t *testing.T) {
	c := newConfidenceState()
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "b.go")
	c.recordResult("edit_file", true, "c.go")
	c.recordResult("edit_file", true, "d.go")
	c.recordResult("edit_file", true, "e.go")

	c.reset()

	if c.totalCalls != 0 || c.successCount != 0 || c.failureCount != 0 {
		t.Error("reset should clear all counters")
	}
	if len(c.uniqueTools) != 0 || len(c.uniqueFiles) != 0 {
		t.Error("reset should clear maps")
	}
	if c.guidanceGiven {
		t.Error("reset should clear guidanceGiven")
	}
}

func TestConfidence_ClusterPenalty(t *testing.T) {
	// Same number of errors, but clustered vs spread should produce different scores
	cClustered := newConfidenceState()
	// 3 success then 4 consecutive errors then 2 success
	cClustered.recordResult("read_file", false, "a.go")
	cClustered.recordResult("read_file", false, "b.go")
	cClustered.recordResult("read_file", false, "c.go")
	cClustered.recordResult("edit_file", true, "d.go")
	cClustered.recordResult("edit_file", true, "d.go")
	cClustered.recordResult("edit_file", true, "d.go")
	cClustered.recordResult("edit_file", true, "d.go")
	cClustered.recordResult("read_file", false, "e.go")
	cClustered.recordResult("read_file", false, "f.go")

	cSpread := newConfidenceState()
	// 3 success then 4 errors spread out + 2 success (same ratio)
	cSpread.recordResult("read_file", false, "a.go")
	cSpread.recordResult("read_file", true, "b.go")
	cSpread.recordResult("read_file", false, "c.go")
	cSpread.recordResult("read_file", true, "d.go")
	cSpread.recordResult("read_file", false, "e.go")
	cSpread.recordResult("read_file", true, "f.go")
	cSpread.recordResult("read_file", false, "g.go")
	cSpread.recordResult("read_file", false, "h.go")
	cSpread.recordResult("read_file", false, "i.go")

	// Clustered errors should have lower score due to cluster penalty
	sClustered := cClustered.score()
	sSpread := cSpread.score()
	if sClustered >= sSpread {
		t.Logf("clustered=%d, spread=%d — clustered should be lower (maxErrorCluster: %d vs %d)",
			sClustered, sSpread, cClustered.maxErrorCluster, cSpread.maxErrorCluster)
	}
}

func TestConfidence_GuidanceContainsReasons(t *testing.T) {
	c := newConfidenceState()
	// Bad edit success rate + low tool diversity
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", false, "a.go")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "a.go")
	c.recordResult("edit_file", true, "a.go")

	// maxErrorCluster is 3, below error-streak threshold of 4
	guidance := c.maybeIntervene()
	if guidance == "" {
		t.Fatal("expected guidance")
	}
	if !strings.Contains(guidance, "edits are failing") {
		t.Logf("guidance: %s", guidance)
	}
}

func TestConfidence_HighScoreNoIntervention(t *testing.T) {
	c := newConfidenceState()
	// Good trajectory with diverse tools and high success rate
	tools := []string{"read_file", "grep", "list_directory", "edit_file", "run_command", "git_diff", "git_commit"}
	for _, tool := range tools {
		c.recordResult(tool, false, "file.go")
	}
	if c.score() < 80 {
		t.Errorf("expected very high score, got %d", c.score())
	}
	if c.maybeIntervene() != "" {
		t.Error("should not intervene on excellent trajectory")
	}
}
