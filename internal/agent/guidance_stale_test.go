package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeStatLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// statLine builds a legacy-or-current flush record; model may be empty.
func statLine(ts, model, tag string, delivered int) string {
	m := ""
	if model != "" {
		m = `,"model":"` + model + `"`
	}
	return `{"ts":"` + ts + `"` + m + `,"tag":"` + tag + `","delivered":` + strconv.Itoa(delivered) + `,"suppressed":0}`
}

// statLineSuppressed builds a flush record with an explicit suppressed count
// (#3459: budget-starved tags must not be reported as stale).
func statLineSuppressed(ts, model, tag string, delivered, suppressed int) string {
	return `{"ts":"` + ts + `","model":"` + model + `","tag":"` + tag + `","delivered":` +
		strconv.Itoa(delivered) + `,"suppressed":` + strconv.Itoa(suppressed) + `}`
}

// #3459: a tag that fires on the current model every run but is always
// suppressed by the guidance budget (delivered:0, suppressed:N) is still
// ALIVE - reporting it stale would mislabel budget starvation as dead
// weight. Other models keep delivering, so the cross-model contrast holds,
// yet no stale_heuristic may be written for the current model.
func TestAnalyzeStaleGuidanceBudgetStarvedNotStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLineSuppressed("2026-10-06T10:00:"+pad(i), "glm-5.2", "Target Scatter", 0, 1),
			statLine("2026-10-06T11:00:"+pad(i), "glm-5.3", "Target Scatter", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	if got := staleHeuristicSummary(path, "glm-5.2"); len(got) != 0 {
		t.Fatalf("budget-starved tag must not be reported stale, got %v", got)
	}
}

// Ten zero-delivery runs on glm-5.2 for tag X; glm-5.3 still delivers X =>
// stale_heuristic reported for glm-5.2.
func TestAnalyzeStaleGuidanceReportsCrossModelDeadWeight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "Attention Fragmentation", 0),
			statLine("2026-10-05T11:00:"+pad(i), "glm-5.3", "Attention Fragmentation", 1),
		)
	}
	writeStatLines(t, path, lines...)

	analyzeStaleGuidance(path, "glm-5.2")

	got := staleHeuristicSummary(path, "glm-5.2")
	if len(got) != 1 || got[0] != "Attention Fragmentation" {
		t.Fatalf("want stale report for Attention Fragmentation on glm-5.2, got %v", got)
	}
	// glm-5.3 delivers, so it must have no report.
	if s := staleHeuristicSummary(path, "glm-5.3"); len(s) != 0 {
		t.Fatalf("glm-5.3 should not be reported, got %v", s)
	}
}

// Fewer than staleMinRuns runs on the current model => silence, no matter
// what other models do.
func TestAnalyzeStaleGuidanceNeedsMinRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns-1; i++ {
		lines = append(lines,
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "ACT NOW", 0),
			statLine("2026-10-05T11:00:"+pad(i), "glm-5.3", "ACT NOW", 2),
		)
	}
	writeStatLines(t, path, lines...)
	analyzeStaleGuidance(path, "glm-5.2")
	if s := staleHeuristicSummary(path, "glm-5.2"); len(s) != 0 {
		t.Fatalf("under min runs must not report, got %v", s)
	}
}

// Zero-delivery on BOTH models is a project trait (docs workspace never
// trips build checks), not model expiry => no report.
func TestAnalyzeStaleGuidanceNoContrastNoReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "Build Idempotency", 0),
			statLine("2026-10-05T11:00:"+pad(i), "glm-5.3", "Build Idempotency", 0),
		)
	}
	writeStatLines(t, path, lines...)
	analyzeStaleGuidance(path, "glm-5.2")
	if s := staleHeuristicSummary(path, ""); len(s) != 0 {
		t.Fatalf("no cross-model contrast must not report, got %v", s)
	}
}

// A prior stale_heuristic line for (model,tag) suppresses re-reporting.
func TestAnalyzeStaleGuidanceDedup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "ACT NOW", 0),
			statLine("2026-10-05T11:00:"+pad(i), "glm-5.3", "ACT NOW", 2),
		)
	}
	writeStatLines(t, path, lines...)
	analyzeStaleGuidance(path, "glm-5.2")
	if got := len(staleHeuristicSummary(path, "glm-5.2")); got != 1 {
		t.Fatalf("first analysis should report once, got %d", got)
	}
	analyzeStaleGuidance(path, "glm-5.2")
	if got := len(staleHeuristicSummary(path, "glm-5.2")); got != 1 {
		t.Fatalf("second analysis must not duplicate, got %d", got)
	}
}

// Current model delivered the tag at least once => alive, no report.
func TestAnalyzeStaleGuidanceAliveTagNotReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		d := 0
		if i == 0 {
			d = 1
		}
		lines = append(lines,
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "ACT NOW", d),
			statLine("2026-10-05T11:00:"+pad(i), "glm-5.3", "ACT NOW", 2),
		)
	}
	writeStatLines(t, path, lines...)
	analyzeStaleGuidance(path, "glm-5.2")
	if s := staleHeuristicSummary(path, "glm-5.2"); len(s) != 0 {
		t.Fatalf("delivered tag must not be reported, got %v", s)
	}
}

// Legacy records without a model field parse as model "" and must not crash
// the analyzer nor leak into verdicts.
func TestAnalyzeStaleGuidanceLegacyRecordsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidance-stats.jsonl")
	var lines []string
	for i := 0; i < staleMinRuns; i++ {
		lines = append(lines,
			statLine("2026-10-05T09:00:"+pad(i), "", "ACT NOW", 5), // legacy
			statLine("2026-10-05T10:00:"+pad(i), "glm-5.2", "ACT NOW", 0),
		)
	}
	writeStatLines(t, path, lines...)
	analyzeStaleGuidance(path, "glm-5.2")
	// legacy "" has the deliveries, but glm-5.2 has no contrast model -> no report
	if s := staleHeuristicSummary(path, "glm-5.2"); len(s) != 0 {
		t.Fatalf("legacy-only contrast must not report, got %v", s)
	}
}

// Missing file / empty model are silent no-ops.
func TestAnalyzeStaleGuidanceNoopCases(t *testing.T) {
	analyzeStaleGuidance(filepath.Join(t.TempDir(), "missing.jsonl"), "glm-5.2")
	writeStatLines(t, filepath.Join(t.TempDir(), "e.jsonl"))
	analyzeStaleGuidance("", "")
}

func pad(i int) string {
	s := "00"
	if i < 10 {
		return s[:1] + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
