package runeval

// Aggregate implements cross-session trajectory aggregation: the Langfuse
// "experiment comparison" layer on top of the per-session scorecard that
// Evaluate produces. Where /runreport answers "how clean was THIS run",
// Aggregate answers "how are my sessions trending overall, which runs were
// the most wasteful, and which tool+input keys keep being re-fetched across
// session boundaries" - questions a single-session report cannot answer.
//
// Pure functions, zero new dependencies, no LLM calls.

import (
	"fmt"
	"sort"
	"strings"
)

// SessionReport pairs one session's identity with its evaluated Report.
type SessionReport struct {
	ID     string
	Title  string
	Report Report
}

// SessionWorst is one entry of the worst-sessions leaderboard.
type SessionWorst struct {
	ID    string
	Title string
	Score int
}

// RepeatedToolAgg aggregates one tool's cross-session duplicate pressure.
type RepeatedToolAgg struct {
	Tool     string
	Repeats  int // sum of DuplicateGroups.Repeats across sessions
	Sessions int // sessions that had at least one duplicate group for it
}

// AggregateReport is the cross-session rollup.
type AggregateReport struct {
	SessionCount     int
	SkippedEmpty     int // sessions with zero tool calls (excluded from means)
	MeanScore        int
	MinScore         int
	MaxScore         int
	WorstSessions    []SessionWorst
	TopRepeatedTools []RepeatedToolAgg

	// Global totals across evaluated sessions.
	ToolCalls           int
	ToolErrors          int
	WastedRepeatCalls   int
	WastedTokenEstimate int
	OverheadTokens      int
	TotalTokens         int

	Findings []string
}

// Aggregate rolls up per-session reports. Sessions whose Report has zero
// tool calls are counted in SkippedEmpty and excluded from score statistics
// (an empty session has no trajectory to grade and would drag the mean).
func Aggregate(sessions []SessionReport) AggregateReport {
	var a AggregateReport
	a.MinScore, a.MaxScore = -1, -1
	scoreSum, scored := 0, 0
	toolRepeats := map[string]*RepeatedToolAgg{}

	for _, s := range sessions {
		a.SessionCount++
		r := s.Report
		if r.ToolCalls == 0 {
			a.SkippedEmpty++
			continue
		}
		scored++
		scoreSum += r.EfficiencyScore
		if a.MinScore < 0 || r.EfficiencyScore < a.MinScore {
			a.MinScore = r.EfficiencyScore
		}
		if r.EfficiencyScore > a.MaxScore {
			a.MaxScore = r.EfficiencyScore
		}
		a.ToolCalls += r.ToolCalls
		a.ToolErrors += r.ToolErrors
		a.WastedRepeatCalls += r.WastedRepeatCalls
		a.WastedTokenEstimate += r.WastedTokenEstimate
		a.OverheadTokens += r.OverheadTokens
		a.TotalTokens += r.TotalTokens

		for _, g := range r.DuplicateGroups {
			agg, ok := toolRepeats[g.Tool]
			if !ok {
				agg = &RepeatedToolAgg{Tool: g.Tool}
				toolRepeats[g.Tool] = agg
			}
			agg.Repeats += g.Repeats
			agg.Sessions++
		}

		a.WorstSessions = append(a.WorstSessions, SessionWorst{
			ID: s.ID, Title: s.Title, Score: r.EfficiencyScore})
	}

	if scored > 0 {
		a.MeanScore = scoreSum / scored
	}

	// Worst sessions leaderboard: score ascending, top 5.
	sort.Slice(a.WorstSessions, func(i, j int) bool {
		if a.WorstSessions[i].Score != a.WorstSessions[j].Score {
			return a.WorstSessions[i].Score < a.WorstSessions[j].Score
		}
		return a.WorstSessions[i].ID < a.WorstSessions[j].ID
	})
	if len(a.WorstSessions) > 5 {
		a.WorstSessions = a.WorstSessions[:5]
	}

	// Most cross-session repeated tools: repeats desc, top 5.
	for _, agg := range toolRepeats {
		a.TopRepeatedTools = append(a.TopRepeatedTools, *agg)
	}
	sort.Slice(a.TopRepeatedTools, func(i, j int) bool {
		if a.TopRepeatedTools[i].Repeats != a.TopRepeatedTools[j].Repeats {
			return a.TopRepeatedTools[i].Repeats > a.TopRepeatedTools[j].Repeats
		}
		return a.TopRepeatedTools[i].Tool < a.TopRepeatedTools[j].Tool
	})
	if len(a.TopRepeatedTools) > 5 {
		a.TopRepeatedTools = a.TopRepeatedTools[:5]
	}

	a.Findings = aggregateFindings(a)
	return a
}

// aggregateFindings builds the cross-session improvement hints, worst-first.
func aggregateFindings(a AggregateReport) []string {
	var f []string
	if a.SessionCount == 0 {
		return f
	}
	if a.SkippedEmpty == a.SessionCount {
		f = append(f, "no session in the window contained any tool calls - nothing to grade")
		return f
	}

	grade := "good"
	switch {
	case a.MeanScore >= 90:
		grade = "excellent"
	case a.MeanScore < 60:
		grade = "needs work"
	}
	f = append(f, fmt.Sprintf(
		"mean trajectory efficiency %d/100 (%s) across %d scored session%s",
		a.MeanScore, grade, a.SessionCount-a.SkippedEmpty, plural(a.SessionCount-a.SkippedEmpty)))

	if len(a.WorstSessions) > 0 && a.WorstSessions[0].Score < 60 {
		w := a.WorstSessions[0]
		title := w.Title
		if title == "" {
			title = w.ID
		}
		f = append(f, fmt.Sprintf(
			"worst session %q scored %d/100 - review it for the failure pattern before it spreads to new sessions",
			title, w.Score))
	}
	if a.ToolCalls > 0 {
		errRate := 100 * a.ToolErrors / a.ToolCalls
		if errRate >= 20 {
			f = append(f, fmt.Sprintf(
				"%d%% of tool calls errored across sessions - persistent environment or argument problems, worth a runbook fix",
				errRate))
		}
	}
	if len(a.TopRepeatedTools) > 0 && a.TopRepeatedTools[0].Repeats >= 5 {
		t := a.TopRepeatedTools[0]
		f = append(f, fmt.Sprintf(
			"tool %q re-fetched %d redundant time%s across %d session%s - cache its result or pin its output in context",
			t.Tool, t.Repeats, plural(t.Repeats), t.Sessions, plural(t.Sessions)))
	}
	return f
}

// RenderAggregate formats the rollup in the same compact scorecard style as
// Render, for /runreport all chat display.
func RenderAggregate(a AggregateReport) string {
	var b strings.Builder
	if a.SessionCount == 0 {
		b.WriteString("Run report (all sessions) - no sessions found\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Run report (all sessions) - %d session%s, %d scored, %d empty\n",
		a.SessionCount, plural(a.SessionCount), a.SessionCount-a.SkippedEmpty, a.SkippedEmpty)
	if a.SessionCount-a.SkippedEmpty > 0 {
		fmt.Fprintf(&b, "  Efficiency: mean %d/100 · min %d · max %d\n",
			a.MeanScore, a.MinScore, a.MaxScore)
	}
	fmt.Fprintf(&b, "  Totals: %d tool calls · %d errored · %d wasted repeats · ~%s wasted context tax (est.)\n",
		a.ToolCalls, a.ToolErrors, a.WastedRepeatCalls, comma(a.WastedTokenEstimate))
	if a.TotalTokens > 0 {
		share := 100 * float64(a.OverheadTokens) / float64(a.TotalTokens)
		fmt.Fprintf(&b, "  Tokens: %s total · %.0f%% overhead machinery\n",
			comma(a.TotalTokens), share)
	}
	if len(a.WorstSessions) > 0 {
		b.WriteString("  Worst sessions:\n")
		for _, w := range a.WorstSessions {
			title := w.Title
			if title == "" {
				title = w.ID
			}
			fmt.Fprintf(&b, "    %3d/100  %s\n", w.Score, ellipsize(title, 60))
		}
	}
	if len(a.TopRepeatedTools) > 0 {
		b.WriteString("  Most cross-session repeated tools:\n")
		for _, t := range a.TopRepeatedTools {
			fmt.Fprintf(&b, "    %s ×%d repeats in %d session%s\n",
				t.Tool, t.Repeats, t.Sessions, plural(t.Sessions))
		}
	}
	if len(a.Findings) > 0 {
		b.WriteString("Findings:\n")
		for _, s := range a.Findings {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	return b.String()
}

// ellipsize truncates s to max runes with an ellipsis marker.
func ellipsize(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
