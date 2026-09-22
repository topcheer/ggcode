package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sa-27: harness-version gating for perf regression detection.
//
// perf-baseline.json mixes runs from any harness into one rolling window.
// After a harness change (new integrity check, prompt edit, tool
// registration) the baseline median and the recent-run consensus were
// computed across different harness versions, producing false regressions
// (baseline=6 iterations under a small harness vs recent=16 under a large
// one) and masking real ones in the other direction. Gating: only entries
// whose HarnessSum equals the current agent fingerprint participate in the
// comparison; entries recorded before the field existed (empty HarnessSum)
// never match and age out of the window.

// harnessGatedAgent builds a bare test agent whose fingerprint is stable
// (empty prompt, nil tool registry) and returns it with the sum that
// recordPerfBaseline should be called with for it.
func harnessGatedAgent(dir string) (*Agent, string) {
	a := &Agent{
		workingDir:     dir,
		contextManager: &fakePerfCM{},
		perfBaseline:   newPerfBaselineState(),
	}
	return a, a.ComputeHarnessFingerprint().Sum()
}

// writeHarnessRuns overwrites .ggcode/perf-baseline.json with one entry per
// (harnessSum, iterations) pair, all successful with identical shape.
func writeHarnessRuns(t *testing.T, dir string, pairs [][2]string) {
	t.Helper()
	entries := make([]string, 0, len(pairs))
	for _, p := range pairs {
		entries = append(entries, fmt.Sprintf(
			`{"iter":%s,"tc":20,"dur":60,"ok":true,"hs":%q}`, p[1], p[0]))
	}
	body := `{"runs":[` + strings.Join(entries, ",") + "]}"
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir .ggcode: %v", err)
	}
	if err := os.WriteFile(perfBaselinePath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
}

// TestPerfRegressionSkipsCrossHarnessHistory verifies that a fully regressed
// history recorded under a DIFFERENT harness never triggers a warning: the
// current harness has fewer than perfBaselineMinRuns same-version runs.
func TestPerfRegressionSkipsCrossHarnessHistory(t *testing.T) {
	dir := t.TempDir()
	a, curSum := harnessGatedAgent(dir)

	old := [][2]string{
		{"old-harness", "10"}, {"old-harness", "10"}, {"old-harness", "10"},
		{"old-harness", "20"}, {"old-harness", "22"}, {"old-harness", "10"},
	}
	writeHarnessRuns(t, dir, old)

	a.maybeInjectPerfRegression()
	if got := len(a.contextManager.(*fakePerfCM).added); got != 0 {
		t.Fatalf("cross-harness regressed history must not warn, got %d messages", got)
	}

	// Once enough CURRENT-harness healthy runs exist, still no warning.
	healthy := append(old, [][2]string{
		{curSum, "10"}, {curSum, "10"}, {curSum, "10"},
		{curSum, "10"}, {curSum, "10"}, {curSum, "11"},
	}...)
	writeHarnessRuns(t, dir, healthy)

	a.perfBaseline.reset()
	a.maybeInjectPerfRegression()
	if got := len(a.contextManager.(*fakePerfCM).added); got != 0 {
		t.Fatalf("healthy current-harness history must not warn, got %d messages", got)
	}
}

// TestPerfRegressionFiresOnCurrentHarnessConsensus verifies the positive
// path: a sustained regression under the CURRENT harness still warns even
// when old-harness healthy runs surround it in the window.
func TestPerfRegressionFiresOnCurrentHarnessConsensus(t *testing.T) {
	dir := t.TempDir()
	a, curSum := harnessGatedAgent(dir)

	runs := [][2]string{
		{curSum, "10"}, {curSum, "10"}, {curSum, "10"},
		{curSum, "10"}, {curSum, "10"},
		{"old-harness", "10"}, {"old-harness", "10"}, {"old-harness", "10"},
		{curSum, "20"}, {curSum, "22"}, {curSum, "20"},
	}
	writeHarnessRuns(t, dir, runs)

	a.maybeInjectPerfRegression()
	if got := len(a.contextManager.(*fakePerfCM).added); got != 1 {
		t.Fatalf("current-harness consensus regression must warn once, got %d messages", got)
	}
}

// TestRecordPerfBaselineStoresHarnessSum verifies the recorded entry carries
// the harness sum so the gating key actually round-trips through disk.
func TestRecordPerfBaselineStoresHarnessSum(t *testing.T) {
	dir := t.TempDir()
	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 3},
		Iterations: 5,
		Duration:   1,
		Success:    true,
	}
	recordPerfBaseline(dir, stats, "fp-abc")
	loaded := loadPerfBaseline(dir)
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	if loaded[0].HarnessSum != "fp-abc" {
		t.Fatalf("HarnessSum not persisted: got %q, want %q", loaded[0].HarnessSum, "fp-abc")
	}
}

// TestPerfBaselineLegacyEntriesNeverMatch guards the migration path: entries
// recorded before the HarnessSum field existed (empty string) must never
// match a real fingerprint, or legacy runs would silently poison baselines.
func TestPerfBaselineLegacyEntriesNeverMatch(t *testing.T) {
	dir := t.TempDir()
	a, curSum := harnessGatedAgent(dir)
	if curSum == "" {
		t.Fatal("computed fingerprint sum must not be empty")
	}

	// Six healthy-looking runs with NO hs field (legacy format).
	writeHarnessRuns(t, dir, [][2]string{
		{"", "10"}, {"", "10"}, {"", "10"}, {"", "10"}, {"", "10"}, {"", "10"},
	})
	a.maybeInjectPerfRegression()
	if got := len(a.contextManager.(*fakePerfCM).added); got != 0 {
		t.Fatalf("legacy (empty HarnessSum) entries must be excluded, got %d messages", got)
	}
}

// TestPerfBaselineHarnessSumOmitEmpty keeps the on-disk format compact for
// legacy-shaped writers: an empty sum must marshal as an absent key.
func TestPerfBaselineHarnessSumOmitEmpty(t *testing.T) {
	b, err := json.Marshal(perfBaselineEntry{Iterations: 1, Success: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "hs") {
		t.Fatalf("empty HarnessSum must be omitted, got %s", b)
	}
}
