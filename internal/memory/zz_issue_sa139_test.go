package memory

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

	am.RecordOutcome("run-insights", "success")
	metas, _ = am.collectMetas()
	if metas[0].Outcome != "success" {
		t.Fatalf("expected outcome overwrite to success, got %q", metas[0].Outcome)
	}

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
	sB := memoryTrustScore(base, now)
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

func TestArbitrateInline_OutcomeBeatsRecency(t *testing.T) {

	now := time.Now()
	failedFresh := MemoryEntry{
		Key:     "deploy-failed-run",
		Content: "deploy command: make deploy",
		Meta:    MemoryMeta{Key: "deploy-failed-run", Category: CategoryPersistent, CreatedAt: now.Add(-1 * time.Hour), Outcome: "failed"},
	}
	verifiedOlder := MemoryEntry{
		Key:     "deploy-verified-run",
		Content: "deploy command: make release deploy",
		Meta:    MemoryMeta{Key: "deploy-verified-run", Category: CategoryPersistent, CreatedAt: now.Add(-40 * 24 * time.Hour), Outcome: "success"},
	}
	arb := ArbitrateInline([]MemoryEntry{failedFresh, verifiedOlder}, now)
	if !arb.HasConflicts() || len(arb.Conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %+v", arb.Conflicts)
	}
	c := arb.Conflicts[0]

	if c.WinnerKey != verifiedOlder.Key || c.LoserKey != failedFresh.Key {
		t.Fatalf("winner=%s loser=%s; want verified success to beat fresh failure", c.WinnerKey, c.LoserKey)
	}
}

func TestRetrieveRanksSuccessAboveFailedAtEqualRelevance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "experience")
	es := NewExperienceStore(dir)

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

	if scored[1].Outcome != "failed" {
		t.Fatalf("expected failed case second, got %q", scored[1].Outcome)
	}
	if scored[0].Score <= scored[1].Score {
		t.Fatalf("success score (%v) must exceed failed score (%v)", scored[0].Score, scored[1].Score)
	}
}
