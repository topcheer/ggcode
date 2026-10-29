package runeval

// Aggregation probes: cross-session rollup correctness (mean/min/max,
// worst-sessions ordering, cross-session repeated-tool aggregation, empty
// and zero-tool-call handling).

import (
	"strings"
	"testing"
)

func TestAggregateEmptyInput(t *testing.T) {
	a := Aggregate(nil)
	if a.SessionCount != 0 || a.MeanScore != 0 || len(a.WorstSessions) != 0 {
		t.Fatalf("empty input should aggregate to zero state: %+v", a)
	}
	if !strings.Contains(RenderAggregate(a), "no sessions found") {
		t.Fatalf("render should state no sessions: %q", RenderAggregate(a))
	}
}

func TestAggregateZeroToolCallSessionsAreSkippedFromMeans(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "a", Title: "empty", Report: Report{ToolCalls: 0, EfficiencyScore: 0}},
		{ID: "b", Title: "real", Report: Report{ToolCalls: 10, EfficiencyScore: 90}},
	})
	if a.SessionCount != 2 || a.SkippedEmpty != 1 {
		t.Fatalf("session/skip counts wrong: %+v", a)
	}
	if a.MeanScore != 90 {
		t.Fatalf("mean should exclude empty session, got %d", a.MeanScore)
	}
	if a.MinScore != 90 || a.MaxScore != 90 {
		t.Fatalf("min/max wrong: %d/%d", a.MinScore, a.MaxScore)
	}
	// The empty session must not appear in the worst leaderboard.
	if len(a.WorstSessions) != 1 || a.WorstSessions[0].ID != "b" {
		t.Fatalf("worst leaderboard should exclude empty: %+v", a.WorstSessions)
	}
}

func TestAggregateMeanMinMaxAndWorstOrdering(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Title: "one", Report: Report{ToolCalls: 10, EfficiencyScore: 80}},
		{ID: "s2", Title: "two", Report: Report{ToolCalls: 10, EfficiencyScore: 40}},
		{ID: "s3", Title: "three", Report: Report{ToolCalls: 10, EfficiencyScore: 95}},
	})
	if a.MeanScore != (80+40+95)/3 {
		t.Fatalf("mean wrong: %d", a.MeanScore)
	}
	if a.MinScore != 40 || a.MaxScore != 95 {
		t.Fatalf("min/max wrong: %d/%d", a.MinScore, a.MaxScore)
	}
	// Ascending score order in the leaderboard.
	if a.WorstSessions[0].ID != "s2" || a.WorstSessions[2].ID != "s3" {
		t.Fatalf("worst ordering wrong: %+v", a.WorstSessions)
	}
	// Worst session scored < 60 must produce a named finding.
	found := false
	for _, f := range a.Findings {
		if strings.Contains(f, `session "two" scored 40/100`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected worst-session finding, got %+v", a.Findings)
	}
}

func TestAggregateCrossSessionRepeatedTools(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Report: Report{ToolCalls: 5, EfficiencyScore: 70, DuplicateGroups: []DuplicateGroup{
			{Tool: "read_file", Count: 4, Repeats: 3},
			{Tool: "grep", Count: 2, Repeats: 1},
		}}},
		{ID: "s2", Report: Report{ToolCalls: 5, EfficiencyScore: 70, DuplicateGroups: []DuplicateGroup{
			{Tool: "read_file", Count: 3, Repeats: 2},
		}}},
	})
	if len(a.TopRepeatedTools) != 2 {
		t.Fatalf("expected two aggregated tools: %+v", a.TopRepeatedTools)
	}
	top := a.TopRepeatedTools[0]
	if top.Tool != "read_file" || top.Repeats != 5 || top.Sessions != 2 {
		t.Fatalf("read_file aggregation wrong: %+v", top)
	}
	// High cross-session repeat pressure must fire a finding.
	found := false
	for _, f := range a.Findings {
		if strings.Contains(f, `"read_file" re-fetched 5 redundant`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected repeated-tool finding, got %+v", a.Findings)
	}
}

func TestAggregateTotalsSumAcrossSessions(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Report: Report{ToolCalls: 20, ToolErrors: 2, WastedRepeatCalls: 4,
			WastedTokenEstimate: 100, OverheadTokens: 500, TotalTokens: 2000, EfficiencyScore: 70}},
		{ID: "s2", Report: Report{ToolCalls: 10, ToolErrors: 1, WastedRepeatCalls: 1,
			WastedTokenEstimate: 50, OverheadTokens: 300, TotalTokens: 1000, EfficiencyScore: 70}},
	})
	if a.ToolCalls != 30 || a.ToolErrors != 3 || a.WastedRepeatCalls != 5 ||
		a.WastedTokenEstimate != 150 || a.OverheadTokens != 800 || a.TotalTokens != 3000 {
		t.Fatalf("totals wrong: %+v", a)
	}
	// 10% error rate is below the 20% finding threshold - no error finding.
	for _, f := range a.Findings {
		if strings.Contains(f, "errored across sessions") {
			t.Fatalf("unexpected error-rate finding at 10%%: %+v", a.Findings)
		}
	}
}

func TestAggregateHighErrorRateFiresFinding(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Report: Report{ToolCalls: 10, ToolErrors: 5, EfficiencyScore: 50}},
	})
	found := false
	for _, f := range a.Findings {
		if strings.Contains(f, "50% of tool calls errored") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error-rate finding, got %+v", a.Findings)
	}
}

func TestAggregateAllEmptySessionsFinding(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "a", Report: Report{}},
		{ID: "b", Report: Report{}},
	})
	if a.SkippedEmpty != 2 || len(a.Findings) != 1 || !strings.Contains(a.Findings[0], "no session in the window contained any tool calls") {
		t.Fatalf("all-empty handling wrong: %+v", a)
	}
}

func TestRenderAggregateShape(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Title: "Fix login bug", Report: Report{ToolCalls: 12, ToolErrors: 1,
			WastedRepeatCalls: 3, WastedTokenEstimate: 900, OverheadTokens: 1000,
			TotalTokens: 8000, EfficiencyScore: 72, DuplicateGroups: []DuplicateGroup{
				{Tool: "grep", Count: 4, Repeats: 3},
			}}},
	})
	out := RenderAggregate(a)
	for _, want := range []string{
		"Run report (all sessions)",
		"mean 72/100",
		"12 tool calls",
		"3 wasted repeats",
		"Worst sessions:",
		"Most cross-session repeated tools:",
		"grep ×3 repeats in 1 session",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q in:\n%s", want, out)
		}
	}
}
