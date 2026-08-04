package agent

// Run Efficiency Report - Post-Run Anti-Pattern Detection
//
// Trend: AI coding agents (Claude Code, Cursor, Devin) increasingly analyze
// their own session performance. Devin generates post-session reports; Claude
// Code tracks token usage; Aider shows diff stats. None analyze HOW EFFICIENTLY
// the agent worked - they all report WHAT happened.
//
// Gap: ggcode's GenerateInsights() produces a descriptive summary (tools used,
// files edited, commands run). It never assesses efficiency: did the agent
// waste iterations? re-read files excessively? have a high error rate? Without
// these signals, the same anti-patterns repeat across sessions.
//
// This module computes deterministic efficiency metrics from RunStats and
// generates actionable recommendations. Zero LLM cost. The recommendations are
// appended to the run reflection saved to project memory, so future sessions
// see lessons from past runs.
//
// Metrics analyzed:
//   1. Edit-to-iteration ratio: low ratio means excessive thinking/reading
//   2. Read amplification: re-reading same files wastes context budget
//   3. Error rate: high failure rate signals struggle or wrong approach
//   4. Context pressure: near-limit context causes quality degradation
//   5. Compaction waste: frequent compactions indicate poor context planning
//
// Integration: called from GenerateInsights() in reflection.go. Returns a
// compact "[Efficiency Analysis]" section appended to the run reflection.

import (
	"fmt"
	"sort"
	"strings"
)

// efficiencyLevel rates overall run efficiency.
type efficiencyLevel int

const (
	efficiencyGood efficiencyLevel = iota // no anti-patterns
	efficiencyFair                        // 1 anti-pattern
	efficiencyPoor                        // 2+ anti-patterns
)

// EfficiencyReport holds the analysis result.
type EfficiencyReport struct {
	Level           efficiencyLevel
	Score           int // 0-100, higher is better
	AntiPatterns    []string
	Recommendations []string
}

// effCheckResult holds a single anti-pattern finding.
type effCheckResult struct {
	deduct         int
	antiPattern    string
	recommendation string
}

// checkEditRatio flags low edit-to-iteration ratio (excessive exploration).
func checkEditRatio(edits, iters int) (effCheckResult, bool) {
	if edits == 0 || iters <= 5 {
		return effCheckResult{}, false
	}
	ratio := float64(edits) / float64(iters)
	if ratio >= 0.15 {
		return effCheckResult{}, false
	}
	return effCheckResult{
		deduct:         25,
		antiPattern:    fmt.Sprintf("Low edit-to-iteration ratio (%d edits / %d iterations = %.2f)", edits, iters, ratio),
		recommendation: "Plan file edits before starting - excessive iterations on few edits suggests exploration loops.",
	}, true
}

// checkReadAmplification flags excessive reads without edits when errors present.
func checkReadAmplification(reads, edits, errors int) (effCheckResult, bool) {
	if reads < 8 || edits > 0 || errors == 0 {
		return effCheckResult{}, false
	}
	return effCheckResult{
		deduct:         15,
		antiPattern:    fmt.Sprintf("High read count (%d) with no edits and %d errors", reads, errors),
		recommendation: "After reading, form a concrete plan before attempting edits to avoid trial-and-error cycles.",
	}, true
}

// checkErrorRate flags high tool failure rate.
func checkErrorRate(totalCalls, errors int) (effCheckResult, bool) {
	if totalCalls <= 5 || errors == 0 {
		return effCheckResult{}, false
	}
	errRate := float64(errors) / float64(totalCalls) * 100
	if errRate <= 40 {
		return effCheckResult{}, false
	}
	return effCheckResult{
		deduct:         20,
		antiPattern:    fmt.Sprintf("High tool error rate (%d/%d = %.0f%%)", errors, totalCalls, errRate),
		recommendation: "Check file existence and read files before editing to reduce edit failures.",
	}, true
}

// checkContextPressure flags near-limit context usage.
func checkContextPressure(peakTokens, window int) (effCheckResult, bool) {
	if window <= 0 || peakTokens <= 0 {
		return effCheckResult{}, false
	}
	peakPct := float64(peakTokens) / float64(window) * 100
	if peakPct <= 85 {
		return effCheckResult{}, false
	}
	return effCheckResult{
		deduct:         15,
		antiPattern:    fmt.Sprintf("Context near capacity (%.0f%% of %dK)", peakPct, window/1000),
		recommendation: "Batch reads and avoid re-reading - use targeted offset/limit reads for large files.",
	}, true
}

// checkCompactionWaste flags excessive context compaction events.
func checkCompactionWaste(count int) (effCheckResult, bool) {
	if count < 2 {
		return effCheckResult{}, false
	}
	return effCheckResult{
		deduct:         15,
		antiPattern:    fmt.Sprintf("%d context compaction events", count),
		recommendation: "Act sooner after gathering information - compacting multiple times loses critical context.",
	}, true
}

// AnalyzeEfficiency inspects RunStats and identifies efficiency anti-patterns.
// Returns a report with a score, detected patterns, and actionable advice.
func AnalyzeEfficiency(stats RunStats) EfficiencyReport {
	r := EfficiencyReport{Score: 100}

	totalCalls := stats.totalToolCalls()
	edits := len(stats.FilesEdited)
	reads := stats.ToolCalls["read_file"] + stats.ToolCalls["multi_file_read"]
	errors := len(stats.Errors)
	iters := stats.Iterations

	checks := []func() (effCheckResult, bool){
		func() (effCheckResult, bool) { return checkEditRatio(edits, iters) },
		func() (effCheckResult, bool) { return checkReadAmplification(reads, edits, errors) },
		func() (effCheckResult, bool) { return checkErrorRate(totalCalls, errors) },
		func() (effCheckResult, bool) {
			return checkContextPressure(stats.ContextPeakTokens, stats.ContextWindow)
		},
		func() (effCheckResult, bool) { return checkCompactionWaste(stats.CompactionCount) },
	}

	for _, check := range checks {
		if res, found := check(); found {
			r.Score -= res.deduct
			r.AntiPatterns = append(r.AntiPatterns, res.antiPattern)
			r.Recommendations = append(r.Recommendations, res.recommendation)
		}
	}

	if r.Score < 0 {
		r.Score = 0
	}

	switch len(r.AntiPatterns) {
	case 0:
		r.Level = efficiencyGood
	case 1:
		r.Level = efficiencyFair
	default:
		r.Level = efficiencyPoor
	}

	return r
}

// Format returns a compact "[Efficiency Analysis]" section for the run reflection.
// Returns empty string if the run was efficient (no anti-patterns) and too short
// to warrant analysis.
func (r EfficiencyReport) Format(stats RunStats) string {
	// Only analyze runs with meaningful activity
	totalCalls := stats.totalToolCalls()
	if totalCalls < 5 && len(stats.FilesEdited) == 0 {
		return ""
	}
	// Don't add noise for efficient runs
	if r.Level == efficiencyGood {
		return ""
	}

	levelLabel := map[efficiencyLevel]string{
		efficiencyGood: "Good",
		efficiencyFair: "Fair",
		efficiencyPoor: "Poor",
	}[r.Level]

	var b strings.Builder
	b.WriteString("[Efficiency Analysis]\n")
	fmt.Fprintf(&b, "Score: %d/100 (%s)\n", r.Score, levelLabel)

	// Sort anti-patterns for deterministic output
	patterns := make([]string, len(r.AntiPatterns))
	copy(patterns, r.AntiPatterns)
	sort.Strings(patterns)
	for _, ap := range patterns {
		fmt.Fprintf(&b, "- %s\n", ap)
	}

	if len(r.Recommendations) > 0 {
		b.WriteString("Recommendations:\n")
		recs := make([]string, len(r.Recommendations))
		copy(recs, r.Recommendations)
		sort.Strings(recs)
		for _, rec := range recs {
			fmt.Fprintf(&b, "- %s\n", rec)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
