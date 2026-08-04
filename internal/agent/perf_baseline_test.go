package agent

import (
	"os"
	"path/filepath"
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
