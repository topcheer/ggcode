package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordOutcomePersistsAndAffectsScore(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	for i := 0; i < 3; i++ {
		if err := RecordOutcome("healthy", true); err != nil {
			t.Fatalf("RecordOutcome error = %v", err)
		}
		if err := RecordOutcome("broken", false); err != nil {
			t.Fatalf("RecordOutcome error = %v", err)
		}
	}

	// Same usage count and recency for both; only outcomes differ.
	skillUsageMu.Lock()
	usage, err := loadUsageLocked()
	skillUsageMu.Unlock()
	if err != nil {
		t.Fatalf("loadUsageLocked error = %v", err)
	}
	healthy := usage["healthy"]
	healthy.UsageCount = 5
	usage["healthy"] = healthy
	broken := usage["broken"]
	broken.UsageCount = 5
	usage["broken"] = broken
	skillUsageMu.Lock()
	if err := saveUsageLocked(usage); err != nil {
		skillUsageMu.Unlock()
		t.Fatalf("saveUsageLocked error = %v", err)
	}
	skillUsageMu.Unlock()

	healthyScore := UsageScore("healthy")
	brokenScore := UsageScore("broken")
	if brokenScore >= healthyScore {
		t.Fatalf("broken score = %v, want < healthy score %v", brokenScore, healthyScore)
	}
	if brokenScore <= 0 {
		t.Fatalf("broken score = %v, want > 0 (floor must allow recovery)", brokenScore)
	}
}

func TestOutcomeSnapshotFiltersUnsampled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	skillUsageMu.Lock()
	if err := saveUsageLocked(map[string]skillUsageEntry{
		"used-only": {UsageCount: 3, LastUsedAt: 1},
		"flaky":     {UsageCount: 3, LastUsedAt: 1, SuccessCount: 1, FailureCount: 3},
	}); err != nil {
		skillUsageMu.Unlock()
		t.Fatalf("saveUsageLocked error = %v", err)
	}
	skillUsageMu.Unlock()

	snap := OutcomeSnapshot()
	if _, ok := snap["used-only"]; ok {
		t.Fatalf("used-only should have no recorded outcomes")
	}
	stat, ok := snap["flaky"]
	if !ok {
		t.Fatalf("flaky missing from snapshot")
	}
	if stat.Runs != 4 || stat.Successes != 1 || stat.Failures != 3 {
		t.Fatalf("flaky stat = %+v, want runs=4 successes=1 failures=3", stat)
	}
	if !stat.Failing() {
		t.Fatalf("flaky should be failing (1/4 success rate)")
	}
}

func TestOutcomeStatFailingMinSamples(t *testing.T) {
	// Fewer than the minimum sample count: never failing.
	if (OutcomeStat{Runs: 2, Successes: 0, Failures: 2}).Failing() {
		t.Fatalf("2 failures should not be judged failing (min samples)")
	}
	// Mixed but majority-success: healthy.
	if (OutcomeStat{Runs: 4, Successes: 3, Failures: 1}).Failing() {
		t.Fatalf("3/4 success rate should be healthy")
	}
}

func TestLegacyUsageFileWithoutOutcomeFieldsLoads(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	ggDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	legacy := `{"deploy": {"usage_count": 4, "last_used_at": 1234567890123}}`
	if err := os.WriteFile(filepath.Join(ggDir, "skill_usage.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if score := UsageScore("deploy"); score <= 0 {
		t.Fatalf("UsageScore = %v, want > 0 for legacy entry", score)
	}

	// New outcome fields must coexist with legacy data after a write.
	if err := RecordOutcome("deploy", false); err != nil {
		t.Fatalf("RecordOutcome error = %v", err)
	}
	snap := OutcomeSnapshot()
	if stat, ok := snap["deploy"]; !ok || stat.Failures != 1 || stat.Runs != 1 {
		t.Fatalf("deploy stat = %+v, want runs=1 failures=1", stat)
	}
}
