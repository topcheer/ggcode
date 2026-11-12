package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPerfDurationMetricExcludesWaitWindows pins the #5523 acceptance:
// a run shaped like [run_command:3 fast] + [sleep 46s]x2 records active
// duration only (~8s active, 92s wait) and must NOT trip the duration
// regression metric against a 93s baseline, while a true regression
// (3 slow run_commands, no waits) still trips it.
func TestPerfDurationMetricExcludesWaitWindows(t *testing.T) {
	baseline := perfBaselineEntry{DurationSec: 93, ToolCalls: 10, Iterations: 5}

	// Wait-heavy run: wall ~100s, active ~8s, deliberate sleep 46s x2.
	waitHeavy := perfBaselineEntry{DurationSec: 8, WaitSec: 92, ToolCalls: 5, Iterations: 5}
	for _, m := range collectRunRegressionMetrics(waitHeavy, baseline) {
		if m == "duration" {
			t.Fatalf("wait-heavy run must not hit duration metric (active=8s vs baseline=93s), hits=%v", collectRunRegressionMetrics(waitHeavy, baseline))
		}
	}

	// True regression: 3 slow run_commands, no waits.
	realRegression := perfBaselineEntry{DurationSec: 300, WaitSec: 0, ToolCalls: 3, Iterations: 5}
	hit := false
	for _, m := range collectRunRegressionMetrics(realRegression, baseline) {
		if m == "duration" {
			hit = true
		}
	}
	if !hit {
		t.Fatal("real 3x-slow-command regression must still hit duration metric")
	}
}

// TestRecordPerfBaselineSplitsActiveAndWait verifies the recorder splits
// wall time into active duration + wait bucket (#5523).
func TestRecordPerfBaselineSplitsActiveAndWait(t *testing.T) {
	dir := t.TempDir()
	stats := newRunStats("prompt")
	stats.recordToolCall("run_command")
	stats.recordToolCall("run_command")
	stats.recordToolCall("run_command")
	stats.Duration = 100 * time.Second
	stats.addWaitTime(46 * time.Second)
	stats.addWaitTime(46 * time.Second)

	recordPerfBaseline(dir, stats)

	runs := loadPerfBaseline(dir)
	if len(runs) != 1 {
		t.Fatalf("expected 1 recorded run, got %d", len(runs))
	}
	got := runs[0]
	if got.WaitSec != 92 {
		t.Errorf("WaitSec = %d, want 92 (46+46)", got.WaitSec)
	}
	if got.DurationSec != 8 {
		t.Errorf("DurationSec = %d, want 8 (100 wall - 92 wait)", got.DurationSec)
	}
}

func TestWaitClassToolClassification(t *testing.T) {
	for _, name := range []string{"sleep", "wait_command", "wait_agent"} {
		if !isWaitClassTool(name) {
			t.Errorf("isWaitClassTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"run_command", "read_file", "grep", ""} {
		if isWaitClassTool(name) {
			t.Errorf("isWaitClassTool(%q) = true, want false", name)
		}
	}
}

func TestFormatRunShapeDiagnosticsShowsWait(t *testing.T) {
	hit := perfBaselineEntry{Iterations: 5, ToolCalls: 2, WaitSec: 92}
	out := formatRunShapeDiagnostics(hit, hit.DurationSec)
	if !strings.Contains(out, "wait=92s") {
		t.Fatalf("run-shape diagnostics should surface wait time, got: %s", out)
	}
}

// TestWaitClassToolsDoNotPolluteBaselineFile guards against the recorder
// writing wait data into an unexpected location.
func TestWaitClassToolsDoNotPolluteBaselineFile(t *testing.T) {
	dir := t.TempDir()
	stats := newRunStats("p")
	stats.recordToolCall("run_command")
	stats.recordToolCall("run_command")
	stats.recordToolCall("run_command")
	recordPerfBaseline(dir, stats)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".ggcode" {
			t.Errorf("unexpected top-level entry %q in baseline dir", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "perf-baseline.json")); err != nil {
		t.Fatalf("perf-baseline.json missing: %v", err)
	}
}
