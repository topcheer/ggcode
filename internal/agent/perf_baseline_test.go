package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPerfBaselineSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	runs := []perfBaselineEntry{
		{RunID: "r1", Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 30, Success: true, Timestamp: time.Now().Unix()},
		{RunID: "r2", Iterations: 8, ToolCalls: 15, Errors: 1, DurationSec: 25, Success: true, Timestamp: time.Now().Unix()},
	}
	savePerfBaseline(tmp, runs)
	loaded := loadPerfBaseline(tmp)
	if len(loaded) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(loaded))
	}
	if loaded[0].RunID != "r1" || loaded[1].RunID != "r2" {
		t.Errorf("run IDs mismatch: %+v", loaded)
	}
}

func TestPerfBaselineLoadMissing(t *testing.T) {
	loaded := loadPerfBaseline("/nonexistent/path")
	if loaded != nil {
		t.Errorf("expected nil for missing file, got %v", loaded)
	}
}

func TestPerfBaselineRollingWindow(t *testing.T) {
	tmp := t.TempDir()
	// Save more than perfBaselineMaxRuns entries.
	var runs []perfBaselineEntry
	for i := 0; i < perfBaselineMaxRuns+10; i++ {
		runs = append(runs, perfBaselineEntry{
			RunID:      "r" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Iterations: i, Success: true,
		})
	}
	savePerfBaseline(tmp, runs)
	loaded := loadPerfBaseline(tmp)
	if len(loaded) > perfBaselineMaxRuns {
		t.Errorf("expected at most %d runs, got %d", perfBaselineMaxRuns, len(loaded))
	}
}

func TestMedianInt(t *testing.T) {
	tests := []struct {
		input []int
		want  int
	}{
		{[]int{1, 2, 3}, 2},
		{[]int{1, 2, 3, 4}, 2}, // (2+3)/2 = 2 (integer div)
		{[]int{5}, 5},
		{[]int{10, 20, 30, 40, 50}, 30},
		{[]int{}, 0},
	}
	for _, tc := range tests {
		got := medianInt(tc.input)
		if got != tc.want {
			t.Errorf("medianInt(%v) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestComputeMedianBaseline(t *testing.T) {
	runs := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 30, Success: true},
		{Iterations: 12, ToolCalls: 22, Errors: 1, DurationSec: 35, Success: true},
		{Iterations: 8, ToolCalls: 18, Errors: 0, DurationSec: 25, Success: true},
		{Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 30, Success: true},
		{Iterations: 15, ToolCalls: 25, Errors: 2, DurationSec: 40, Success: true},
	}
	mid := computeMedianBaseline(runs)
	if mid.Iterations != 10 {
		t.Errorf("expected median iterations=10, got %d", mid.Iterations)
	}
	if mid.ToolCalls != 20 {
		t.Errorf("expected median toolCalls=20, got %d", mid.ToolCalls)
	}
}

func TestComputeMedianBaselineSkipsFailed(t *testing.T) {
	runs := []perfBaselineEntry{
		{Iterations: 100, ToolCalls: 200, Errors: 5, Success: false},
		{Iterations: 10, ToolCalls: 20, Errors: 0, Success: true},
		{Iterations: 12, ToolCalls: 22, Errors: 0, Success: true},
		{Iterations: 8, ToolCalls: 18, Errors: 0, Success: true},
		{Iterations: 10, ToolCalls: 20, Errors: 0, Success: true},
		{Iterations: 14, ToolCalls: 24, Errors: 0, Success: true},
	}
	mid := computeMedianBaseline(runs)
	// Should only use 5 successful runs: 8,10,10,12,14 -> median=10
	if mid.Iterations != 10 {
		t.Errorf("expected median iterations=10 (from successful runs), got %d", mid.Iterations)
	}
}

func TestCheckSingleRunRegression(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 30, ContextPeak: 5000}

	// No regression
	run := perfBaselineEntry{Iterations: 12, ToolCalls: 22, Errors: 0, DurationSec: 35, ContextPeak: 6000}
	hit, _ := checkSingleRunRegression(run, baseline)
	if hit {
		t.Error("expected no regression for normal run")
	}

	// Iterations regression: 20 iterations vs 10 baseline (2x > 1.5x)
	run = perfBaselineEntry{Iterations: 20, ToolCalls: 22, Errors: 0, DurationSec: 35}
	hit, metric := checkSingleRunRegression(run, baseline)
	if !hit || metric != "iterations" {
		t.Errorf("expected iterations regression, got hit=%v metric=%s", hit, metric)
	}

	// Duration regression: 60s vs 30s baseline (2x > 1.5x)
	run = perfBaselineEntry{Iterations: 12, ToolCalls: 22, Errors: 0, DurationSec: 60}
	hit, metric = checkSingleRunRegression(run, baseline)
	if !hit || metric != "duration" {
		t.Errorf("expected duration regression, got hit=%v metric=%s", hit, metric)
	}

	// Error rate regression: 0 baseline errors, >5% error rate
	run = perfBaselineEntry{Iterations: 12, ToolCalls: 20, Errors: 2, DurationSec: 35}
	hit, metric = checkSingleRunRegression(run, baseline)
	if !hit || metric != "error_rate" {
		t.Errorf("expected error_rate regression, got hit=%v metric=%s", hit, metric)
	}
}

func TestCheckSingleRunRegressionShortDuration(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, DurationSec: 5}
	run := perfBaselineEntry{Iterations: 11, DurationSec: 20}
	// Baseline duration too short (<=10s) - should not trigger duration regression
	hit, metric := checkSingleRunRegression(run, baseline)
	if hit && metric == "duration" {
		t.Error("should not trigger duration regression when baseline is very short")
	}
}

func TestRecordPerfBaseline(t *testing.T) {
	tmp := t.TempDir()
	stats := &RunStats{
		ToolCalls:   map[string]int{"read_file": 3, "edit_file": 2},
		FilesEdited: []string{"a.go", "b.go"},
		Iterations:  10,
		Duration:    30 * time.Second,
	}
	stats.Success = true

	recordPerfBaseline(tmp, stats)
	loaded := loadPerfBaseline(tmp)
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	if loaded[0].Iterations != 10 {
		t.Errorf("expected 10 iterations, got %d", loaded[0].Iterations)
	}
	if loaded[0].ToolCalls != 5 {
		t.Errorf("expected 5 tool calls, got %d", loaded[0].ToolCalls)
	}
	if loaded[0].DurationSec != 30 {
		t.Errorf("expected 30s duration, got %d", loaded[0].DurationSec)
	}
}

func TestRecordPerfBaselineSkipsTrivial(t *testing.T) {
	tmp := t.TempDir()
	// Run with no meaningful work should be skipped
	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 1},
		Iterations: 1,
	}
	stats.finalize(nil)
	recordPerfBaseline(tmp, stats)

	loaded := loadPerfBaseline(tmp)
	if len(loaded) != 0 {
		t.Errorf("expected 0 entries for trivial run, got %d", len(loaded))
	}
}

func TestFormatPerfRegressionWarning(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, DurationSec: 30}
	latest := perfBaselineEntry{Iterations: 20, DurationSec: 60}

	msg := formatPerfRegressionWarning("iterations", baseline, latest)
	if msg == "" {
		t.Error("expected non-empty warning for iterations regression")
	}
	if !containsStr(msg, "iteration count") {
		t.Errorf("warning should mention 'iteration count', got: %s", msg)
	}
	if !containsStr(msg, "baseline=10") {
		t.Errorf("warning should show baseline=10, got: %s", msg)
	}
}

func TestPerfBaselinePath(t *testing.T) {
	p := perfBaselinePath("/foo/bar")
	expected := filepath.Join("/foo/bar", ".ggcode", "perf-baseline.json")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestPerfBaselineStateReset(t *testing.T) {
	s := newPerfBaselineState()
	s.warnCount = 5
	s.hasBaseline = true
	s.reset()
	if s.warnCount != 0 {
		t.Errorf("expected warnCount=0 after reset, got %d", s.warnCount)
	}
}

func TestIntToStr(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{42, "42"},
		{-5, "-5"},
		{1000, "1000"},
	}
	for _, tc := range tests {
		got := intToStr(tc.input)
		if got != tc.want {
			t.Errorf("intToStr(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTrimZeros(t *testing.T) {
	if trimZeros("1.5") != "1.5" {
		t.Error("trimZeros should not modify '1.5'")
	}
	if trimZeros("2.0") != "2" {
		t.Error("trimZeros should convert '2.0' to '2'")
	}
}

func TestPerfBaselineDataFile(t *testing.T) {
	tmp := t.TempDir()
	// Verify that .ggcode directory is created automatically.
	path := perfBaselinePath(tmp)
	dir := filepath.Dir(path)

	// Directory may not exist yet.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Skip("directory already exists")
	}

	// Save should create it.
	savePerfBaseline(tmp, []perfBaselineEntry{{RunID: "test", Success: true}})
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("expected directory created, got error: %v", err)
	}
}

func TestTopToolMix(t *testing.T) {
	m := map[string]int{"run_command": 48, "read_file": 30, "edit_file": 12, "grep": 30}
	got := topToolMix(m, 3)
	want := []string{"run_command:48", "grep:30", "read_file:30"}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTopToolMixEmpty(t *testing.T) {
	if got := topToolMix(nil, 3); got != nil {
		t.Errorf("expected nil for empty map, got %v", got)
	}
}

func TestFormatPerfRegressionWarningIncludesRunShape(t *testing.T) {
	baseline := perfBaselineEntry{DurationSec: 100, Iterations: 10}
	hit := perfBaselineEntry{
		Iterations: 45, ToolCalls: 120, DurationSec: 982,
		TopTools: []string{"run_command:48", "read_file:30"},
	}
	msg := formatPerfRegressionWarning("duration", baseline, hit)
	for _, want := range []string{"iterations=45", "tool_calls=120", "sec/iter≈21.8", "run_command:48", "read_file:30"} {
		if !strings.Contains(msg, want) {
			t.Errorf("duration advisory missing %q: %s", want, msg)
		}
	}
	msg = formatPerfRegressionWarning("iterations", baseline, hit)
	if !strings.Contains(msg, "run_command:48") {
		t.Errorf("iterations advisory should carry run-shape diagnostics: %s", msg)
	}

	// Legacy baseline recorded before TopTools existed: must not panic or
	// fabricate a top-tools segment.
	legacyMsg := formatPerfRegressionWarning("duration",
		perfBaselineEntry{DurationSec: 100},
		perfBaselineEntry{DurationSec: 200, Iterations: 5})
	if strings.Contains(legacyMsg, "top tools") {
		t.Errorf("legacy entry should not claim top tools: %s", legacyMsg)
	}
}

func TestRecordPerfBaselineCapturesTopTools(t *testing.T) {
	tmp := t.TempDir()
	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 3, "edit_file": 2, "run_command": 5},
		Iterations: 10,
		Duration:   30 * time.Second,
	}
	stats.Success = true
	recordPerfBaseline(tmp, stats)
	loaded := loadPerfBaseline(tmp)
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	want := []string{"run_command:5", "read_file:3", "edit_file:2"}
	if len(loaded[0].TopTools) != len(want) {
		t.Fatalf("expected top tools %v, got %v", want, loaded[0].TopTools)
	}
	for i := range want {
		if loaded[0].TopTools[i] != want[i] {
			t.Errorf("top tool %d: got %q, want %q", i, loaded[0].TopTools[i], want[i])
		}
	}
}

// ---------- Workload normalization for scale-sensitive metrics ----------

func TestScaleSensitiveMetricsNormalizeByWorkload(t *testing.T) {
	// Baseline median blends quick-fix runs; the run under test is a
	// legitimate deep-research session (~2x raw totals across the board).
	// Absolute comparison flags 2.1x "context bloat"; per-call rates
	// (3335 vs 2904 tokens/call, 16.4 vs 15.0 sec/call, 0.71 vs 0.67
	// iters/call) are all well under the 1.5x threshold.
	baseline := perfBaselineEntry{Iterations: 40, ToolCalls: 60, DurationSec: 900, ContextPeak: 174249}
	run := perfBaselineEntry{Iterations: 78, ToolCalls: 110, DurationSec: 1800, ContextPeak: 366866}
	if hit, metric := checkSingleRunRegression(run, baseline); hit {
		t.Fatalf("large-but-efficient run must not be flagged, got metric=%q", metric)
	}

	// Same workload, genuinely bloated context: 2x tokens per call fires.
	bloated := run
	bloated.ContextPeak = 700000
	if hit, metric := checkSingleRunRegression(bloated, baseline); !hit || metric != "context_usage" {
		t.Errorf("expected context_usage regression for bloat at equal workload, got hit=%v metric=%q", hit, metric)
	}

	// Chatty run at baseline-scale workload: 2x iterations per call fires.
	chatty := baseline
	chatty.Iterations = 80
	if hit, metric := checkSingleRunRegression(chatty, baseline); !hit || metric != "iterations" {
		t.Errorf("expected iterations regression for chatty run, got hit=%v metric=%q", hit, metric)
	}
}

func TestLegacyAbsoluteFallbackBelowToolCallFloor(t *testing.T) {
	// Below the per-call floor on either side, legacy absolute semantics
	// apply (also covers synthetic tc=0 entries used by older tests).
	baseline := perfBaselineEntry{Iterations: 10, ToolCalls: 4, DurationSec: 30, ContextPeak: 5000}
	run := perfBaselineEntry{Iterations: 20, ToolCalls: 4, DurationSec: 35, ContextPeak: 6000}
	hit, metric := checkSingleRunRegression(run, baseline)
	if !hit || metric != "iterations" {
		t.Errorf("expected legacy absolute iterations regression below floor, got hit=%v metric=%q", hit, metric)
	}
}

func TestFormatScaleMetricWarningShowsPerCallRateAndTotals(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 40, ToolCalls: 60, ContextPeak: 174249}
	latest := perfBaselineEntry{Iterations: 90, ToolCalls: 60, ContextPeak: 349000}
	msg := formatPerfRegressionWarning("context_usage", baseline, latest)
	if !strings.Contains(msg, "peak context tokens per tool call") {
		t.Errorf("normalized advisory should lead with per-call rate, got: %s", msg)
	}
	if !strings.Contains(msg, "baseline=2904.15") {
		t.Errorf("normalized advisory should show the per-call baseline, got: %s", msg)
	}
	if !strings.Contains(msg, "recent=349000 tokens") {
		t.Errorf("normalized advisory should keep run totals, got: %s", msg)
	}

	// tc=0 entries degrade to the legacy absolute-only line.
	legacyMsg := formatPerfRegressionWarning("context_usage",
		perfBaselineEntry{ContextPeak: 5000}, perfBaselineEntry{ContextPeak: 12000})
	if strings.Contains(legacyMsg, "per tool call") {
		t.Errorf("legacy fallback should not mention per-call rates, got: %s", legacyMsg)
	}
	if !strings.Contains(legacyMsg, "baseline=5000") || !strings.Contains(legacyMsg, "recent=12000") {
		t.Errorf("legacy fallback should show absolute totals, got: %s", legacyMsg)
	}
}
