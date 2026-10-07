package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// tuningSetup points the override store at a temp dir (path-injection
// pattern, mirroring guidance_stale_test.go's explicit-path style) and
// resets process state; restored on cleanup.
func tuningSetup(t *testing.T) (statsPath, overridesPath string) {
	t.Helper()
	dir := t.TempDir()
	statsPath = filepath.Join(dir, "guidance-stats.jsonl")
	overridesPath = filepath.Join(dir, "harness-overrides.json")
	harnessMu.Lock()
	oldPath := harnessPathFn
	harnessPathFn = func() string { return overridesPath }
	harnessOverrides = nil
	harnessConfirmed = map[string]int{}
	harnessMu.Unlock()
	t.Cleanup(func() {
		harnessMu.Lock()
		harnessPathFn = oldPath
		harnessOverrides = nil
		harnessConfirmed = map[string]int{}
		harnessMu.Unlock()
	})
	return statsPath, overridesPath
}

func tuningRunLine(ts, tag string, delivered int) string {
	return fmt.Sprintf(`{"ts":"2026-10-07T10:%s:00Z","model":"glm-5.2","tag":%q,"delivered":%d,"suppressed":0}`, ts, tag, delivered)
}

func TestHarnessTuningAcceptsStaleTagAfterTwoConfirmations(t *testing.T) {
	statsPath, overridesPath := tuningSetup(t)
	var lines []string
	for i := 0; i < 34; i++ {
		tag := "Target Scatter"
		if i >= 30 {
			tag = "Neutral Tag" // trailing followup runs, no error marks
		}
		delivered := 1
		if i >= 30 {
			delivered = 0
		}
		lines = append(lines, tuningRunLine(pad(i), tag, delivered))
	}
	writeStatLines(t, statsPath, lines...)

	// First confirmation: proposal only, nothing persisted.
	if err := MaybeTuneHarness([]string{"Target Scatter"}, statsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(overridesPath); !os.IsNotExist(err) {
		t.Fatal("override persisted after a single confirmation")
	}
	// Second confirmation: gate passes (no post-delivery error rise) -> persist.
	if err := MaybeTuneHarness([]string{"Target Scatter"}, statsPath); err != nil {
		t.Fatal(err)
	}
	ov := loadHarnessOverrides(overridesPath)
	o, ok := ov["Target Scatter"]
	if !ok || o.Tier != 0 || o.Model != "glm-5.2" {
		t.Fatalf("want suppress override for Target Scatter, got %+v", ov)
	}
	wrapped := ApplyOverridesToAllow(func(string) bool { return true })
	if wrapped("Target Scatter") {
		t.Fatal("overridden tag must be suppressed by the wrapped allow")
	}
	if !wrapped("Neutral Tag") {
		t.Fatal("non-overridden tag must pass through")
	}
}

func TestHarnessTuningGateRejectsRisingErrorRate(t *testing.T) {
	statsPath, overridesPath := tuningSetup(t)
	var lines []string
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			lines = append(lines, tuningRunLine(pad(i), "Target Scatter", 1))
		} else {
			lines = append(lines, tuningRunLine(pad(i), "Error Cascade", 1))
		}
	}
	writeStatLines(t, statsPath, lines...)

	for i := 0; i < 2; i++ {
		if err := MaybeTuneHarness([]string{"Target Scatter"}, statsPath); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(overridesPath); !os.IsNotExist(err) {
		t.Fatal("gate must reject: every delivered run is followed by error-marked runs")
	}
	if got := proposeTierChange("Target Scatter", readGuidanceStatTail(statsPath)); got {
		t.Fatal("proposeTierChange must reject a rising post-delivery error rate")
	}
	if wrapped := ApplyOverridesToAllow(func(string) bool { return true }); !wrapped("Target Scatter") {
		t.Fatal("rejected tag must not be suppressed")
	}
}

func TestHarnessTuningBudgetCapsPerModel(t *testing.T) {
	statsPath, overridesPath := tuningSetup(t)
	tags := []string{"Tag A", "Tag B", "Tag C", "Tag D"}
	var lines []string
	for i := 0; i < 44; i++ {
		for _, tag := range tags {
			lines = append(lines, tuningRunLine(pad(i), tag, 1))
		}
	}
	writeStatLines(t, statsPath, lines...)

	for i := 0; i < 2; i++ {
		if err := MaybeTuneHarness(tags, statsPath); err != nil {
			t.Fatal(err)
		}
	}
	ov := loadHarnessOverrides(overridesPath)
	if len(ov) != tuningMaxOverrides {
		t.Fatalf("want exactly %d overrides (budget), got %d: %+v", tuningMaxOverrides, len(ov), ov)
	}
	if _, ok := ov["Tag D"]; ok {
		t.Fatal("fourth tag must be rejected by the per-model budget")
	}
}

func TestHarnessTuningRollbackByFileDeletion(t *testing.T) {
	statsPath, overridesPath := tuningSetup(t)
	var lines []string
	for i := 0; i < 34; i++ {
		lines = append(lines, tuningRunLine(pad(i), "Target Scatter", 1))
	}
	writeStatLines(t, statsPath, lines...)
	for i := 0; i < 2; i++ {
		if err := MaybeTuneHarness([]string{"Target Scatter"}, statsPath); err != nil {
			t.Fatal(err)
		}
	}
	if len(loadHarnessOverrides(overridesPath)) != 1 {
		t.Fatal("precondition: one override applied")
	}

	// Rollback: delete the store, drop the process cache (fresh process).
	if err := os.Remove(overridesPath); err != nil {
		t.Fatal(err)
	}
	harnessMu.Lock()
	harnessOverrides = nil
	harnessMu.Unlock()
	if got := loadHarnessOverrides(overridesPath); len(got) != 0 {
		t.Fatalf("deleted store must load as empty map, got %+v", got)
	}
	if wrapped := ApplyOverridesToAllow(func(string) bool { return true }); !wrapped("Target Scatter") {
		t.Fatal("after rollback the tag must be allowed again")
	}
}

// injectGuidance must honor a persisted override even when the budget would
// allow the message: the tuning store gets the final say after the budget
// verdict (sa-109 Tier A consumer-side check - guards the write-only death
// sa-84 flagged for guidancePromoter). Suppression flows back into stats.
func TestHarnessTuningInjectGuidanceHonorsOverride(t *testing.T) {
	_, overridesPath := tuningSetup(t)
	suppressedMsg := "advisory-tune-test dead weight: consider slowing down"
	tag := guidanceTag(suppressedMsg)
	seed := harnessOverride{tag: {Tier: tuningSuppressTier, Model: "glm-5.2"}}
	b, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridesPath, b, 0644); err != nil {
		t.Fatal(err)
	}
	harnessMu.Lock()
	harnessOverrides = nil // force reload from the seeded store
	harnessMu.Unlock()

	a, _ := issue677Agent(t)
	a.guidanceStats = guidanceRunStats{}
	if a.injectGuidance(suppressedMsg) {
		t.Fatal("budget-legal guidance for an overridden tag must be suppressed")
	}
	if st := a.guidanceStats[tag]; st == nil || st.Suppressed == 0 {
		t.Fatalf("suppression must flow into guidanceStats, got %+v", a.guidanceStats)
	}
	if !a.injectGuidance("advisory-free-tag totally fine") {
		t.Fatal("non-overridden tags must pass through untouched")
	}
}
