package agent

// Regression tests for GitHub issues #1143 and #1144.
//
// #1143: perf_baseline regression gate must require >= 2 recent runs to
// regress on the SAME metric (no cross-metric vote pooling), and warning
// values must come from runs that actually hit the winning metric instead of
// blindly using the latest run.
//
// #1144: extractCommandArg must decode JSON properly so escaped quotes
// (\") inside command arguments no longer truncate extraction into a wrong
// but non-empty string that bypassed recordToolCall's fallback.

import (
	"strings"
	"testing"
)

// ---------- Issue #1143: same-metric consensus ----------

func TestIssue1143_CrossMetricVotesDoNotPassConsensusGate(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 60}
	// run1 regresses on iterations, run2 on duration, run3 is clean.
	// Old code accumulated regressionHits>=2 across different metrics and
	// passed the gate anyway (#1143).
	recent3 := []perfBaselineEntry{
		{Iterations: 20, ToolCalls: 22, DurationSec: 40},
		{Iterations: 10, ToolCalls: 20, DurationSec: 120},
		{Iterations: 10, ToolCalls: 20, DurationSec: 40},
	}
	counts := make(map[string]int)
	for _, r := range recent3 {
		if _, m := checkSingleRunRegression(r, baseline); m != "" {
			counts[m]++
		}
	}
	if got := pickConsensusPerfMetric(counts); got != "" {
		t.Fatalf("expected no metric to reach same-metric consensus, got %q (counts=%v)", got, counts)
	}
}

func TestIssue1143_WorstMetricIsConsensusWinnerNotLastLoopValue(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60}
	// Runs 1-2 regress on duration, run 3 regresses on iterations last, so the
	// old loop would overwrite worstMetric to "iterations" (#1143).
	recent3 := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, DurationSec: 100},
		{Iterations: 12, ToolCalls: 20, DurationSec: 120},
		{Iterations: 20, ToolCalls: 20, DurationSec: 50},
	}
	counts := make(map[string]int)
	for _, r := range recent3 {
		if _, m := checkSingleRunRegression(r, baseline); m != "" {
			counts[m]++
		}
	}
	if got := pickConsensusPerfMetric(counts); got != "duration" {
		t.Fatalf("expected consensus winner metric 'duration', got %q (counts=%v)", got, counts)
	}
	hitRun := selectWorstPerfHit(recent3, baseline, "duration")
	if hitRun.DurationSec != 120 {
		t.Errorf("expected most severe hit run duration=120, got %d", hitRun.DurationSec)
	}
}

func TestIssue1143_WarningValuesComeFromHitRunNotLatestRun(t *testing.T) {
	baseline := perfBaselineEntry{Iterations: 10, ToolCalls: 20, Errors: 0, DurationSec: 30}
	// The latest run is healthy (iter=8), so old code produced contradictory
	// copy like "baseline=10, recent=8 (0.8x baseline)" while claiming a
	// regression (#1143).
	recent3 := []perfBaselineEntry{
		{Iterations: 20, ToolCalls: 22, DurationSec: 30},
		{Iterations: 25, ToolCalls: 22, DurationSec: 30},
		{Iterations: 8, ToolCalls: 20, DurationSec: 25},
	}
	counts := make(map[string]int)
	for _, r := range recent3 {
		if _, m := checkSingleRunRegression(r, baseline); m != "" {
			counts[m]++
		}
	}
	worstMetric := pickConsensusPerfMetric(counts)
	if worstMetric != "iterations" {
		t.Fatalf("expected consensus winner 'iterations', got %q (counts=%v)", worstMetric, counts)
	}
	hitRun := selectWorstPerfHit(recent3, baseline, worstMetric)
	if hitRun.Iterations != 25 {
		t.Errorf("expected most severe hit run iterations=25, got %d", hitRun.Iterations)
	}
	msg := formatPerfRegressionWarning(worstMetric, baseline, hitRun)
	if !strings.Contains(msg, "recent=25") {
		t.Errorf("warning should quote the hitting run's value 25, got: %s", msg)
	}
	if strings.Contains(msg, "recent=8") {
		t.Errorf("warning must not quote the healthy latest run's value, got: %s", msg)
	}
}

func TestIssue1143_PickConsensusDeterministicOnTies(t *testing.T) {
	// Two metrics both reach 2 hits: priority order picks deterministically.
	counts := map[string]int{"duration": 2, "iterations": 2}
	for i := 0; i < 20; i++ {
		if got := pickConsensusPerfMetric(counts); got != "iterations" {
			t.Fatalf("iteration %d: expected deterministic winner 'iterations', got %q", i, got)
		}
	}
}

func TestIssue1143_PerfMetricValueMapping(t *testing.T) {
	e := perfBaselineEntry{Iterations: 3, DurationSec: 7, Errors: 5, ContextPeak: 11, Compactions: 13}
	cases := map[string]int{
		"iterations":    3,
		"duration":      7,
		"error_rate":    5,
		"context_usage": 11,
		"compaction":    13,
		"unknown":       0,
	}
	for metric, want := range cases {
		if got := perfMetricValue(e, metric); got != want {
			t.Errorf("perfMetricValue(%q) = %d, want %d", metric, got, want)
		}
	}
}

// ---------- Issue #1144: JSON escaped quotes in extractCommandArg ----------

func TestIssue1144_ExtractCommandArgHandlesEscapedQuotes(t *testing.T) {
	// The exact reproduction case from issue #1144: the agent's argument
	// JSON contains \" sequences that the naive scan truncated at.
	input := `{"command":"echo \"Running tests\" && go test ./..."}`
	want := `echo "Running tests" && go test ./...`
	if got := extractCommandArg(input); got != want {
		t.Fatalf("extractCommandArg() = %q, want %q", got, want)
	}
}

func TestIssue1144_RecordToolCallArmsTestCategoryWithQuotedCompound(t *testing.T) {
	s := newPhantomVerifyState()
	argsJSON := `{"command":"echo \"Running tests\" && go test ./..."}`
	s.recordToolCall("run_command", argsJSON, false)
	if !s.categoriesRun[phantomCatTest] {
		t.Fatal("test category should be armed by a real go test command containing escaped quotes")
	}
	// End-to-end: the same assistant text must NOT be flagged as phantom
	// verification once the category is armed correctly (#1144).
	a := &Agent{phantomVerify: s}
	if hint := a.maybeWarnPhantomVerify("All tests pass."); hint != "" {
		t.Errorf("correctly armed verification must not trigger phantom warning, got: %s", hint)
	}
}

func TestIssue1144_ExtractCommandArgKeepsFallbackSemantics(t *testing.T) {
	tests := []struct {
		name     string
		argsJSON string
		want     string
	}{
		{"malformed json", `{not valid json}`, ""},
		{"missing command field", `{"path":"/tmp/x","content":"y"}`, ""},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractCommandArg(tt.argsJSON); got != tt.want {
				t.Errorf("extractCommandArg(%q) = %q, want %q", tt.argsJSON, got, tt.want)
			}
		})
	}
}
