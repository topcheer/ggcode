package memory

// sa-139 (MemGuard arXiv:2608.21867): verification-outcome lifecycle metadata.
// Four faces: sidecar round-trip, trust-score branch, retrieval rank weight,
// and cross-outcome arbitration ordering.

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordOutcomeSidecarRoundTrip(t *testing.T) {
	am := &AutoMemory{dir: t.TempDir()}
	if err := am.SaveMemory("run-insights", "some insight text"); err != nil {
		t.Fatalf("save: %v", err)
	}
	am.RecordOutcome("run-insights", "failed")
	metas, err := am.collectMetas()
	if err != nil || len(metas) != 1 {
		t.Fatalf("collectMetas: %v metas=%d", err, len(metas))
	}
	if metas[0].Outcome != "failed" {
		t.Fatalf("expected outcome=failed in MemoryMeta, got %q", metas[0].Outcome)
	}
	// Overwrite semantics: a re-written key carries the new run's outcome.
	am.RecordOutcome("run-insights", "success")
	metas, _ = am.collectMetas()
	if metas[0].Outcome != "success" {
		t.Fatalf("expected outcome overwrite to success, got %q", metas[0].Outcome)
	}
	// Invalid outcome labels are not recorded.
	am.RecordOutcome("run-insights", "bogus")
	metas, _ = am.collectMetas()
	if metas[0].Outcome != "success" {
		t.Fatalf("bogus outcome must not overwrite, got %q", metas[0].Outcome)
	}
}

func TestRecordOutcomeCreatesRecordForUnknownKey(t *testing.T) {
	am := &AutoMemory{dir: t.TempDir()}
	am.RecordOutcome("fresh-key", "partial")
	usage := am.loadUsage()
	rec, ok := usage.Entries["fresh-key"]
	if !ok || rec == nil {
		t.Fatal("RecordOutcome must create the sidecar record when absent")
	}
	if rec.Outcome != "partial" {
		t.Fatalf("expected partial, got %q", rec.Outcome)
	}
}

func TestMemoryTrustScoreOutcomeBranch(t *testing.T) {
	now := time.Now()
	base := MemoryMeta{Category: CategoryDefault, CreatedAt: now}
	success := base
	success.Outcome = "success"
	failed := base
	failed.Outcome = "failed"

	sS := memoryTrustScore(success, now)
	sF := memoryTrustScore(failed, now)
	sB := memoryTrustScore(base, now) // unknown outcome: no bonus, no penalty
	if !(sS > sB && sB > sF) {
		t.Fatalf("expected success(%v) > unknown(%v) > failed(%v)", sS, sB, sF)
	}
}

func TestOutcomeRankWeightOrdering(t *testing.T) {
	if !(outcomeRankWeight("success") > outcomeRankWeight("") &&
		outcomeRankWeight("") > outcomeRankWeight("partial") &&
		outcomeRankWeight("partial") > outcomeRankWeight("failed")) {
		t.Fatalf("weight ordering broken: success=%v unknown=%v partial=%v failed=%v",
			outcomeRankWeight("success"), outcomeRankWeight(""), outcomeRankWeight("partial"), outcomeRankWeight("failed"))
	}
}

func TestRetrieveRanksSuccessAboveFailedAtEqualRelevance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "experience")
	es := NewExperienceStore(dir)
	// Two DISTINCT tasks (Record updates a same-shape case instead of adding a
	// new one) sharing the "login flake auth" vocabulary so both clear the
	// relevance gate; query targets the shared vocabulary.
	if _, _, err := es.Record("fix login flake in auth handler", "retry with backoff worked", "success", nil); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if _, _, err := es.Record("stabilize flaky auth login test", "blindly retried, still flaky", "failed", nil); err != nil {
		t.Fatalf("record failed: %v", err)
	}
	scored, err := es.Retrieve("login flake auth", 2)
	if err != nil || len(scored) != 2 {
		t.Fatalf("retrieve: %v n=%d", err, len(scored))
	}
	if scored[0].Outcome != "success" {
		t.Fatalf("expected success case ranked first, got %q", scored[0].Outcome)
	}
	// Failed case must still be retrievable (negative-example value), just lower.
	if scored[1].Outcome != "failed" {
		t.Fatalf("expected failed case second, got %q", scored[1].Outcome)
	}
	if scored[0].Score <= scored[1].Score {
		t.Fatalf("success score (%v) must exceed failed score (%v)", scored[0].Score, scored[1].Score)
	}
}
