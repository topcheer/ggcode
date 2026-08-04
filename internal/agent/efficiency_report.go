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

// AnalyzeEfficiency inspects RunStats and identifies efficiency anti-patterns.
// Returns a report with a score, detected patterns, and actionable advice.
func AnalyzeEfficiency(stats RunStats) EfficiencyReport {
	r := EfficiencyReport{Score: 100}

	totalCalls := stats.totalToolCalls()
	edits := len(stats.FilesEdited)
	reads := stats.ToolCalls["read_file"] + stats.ToolCalls["multi_file_read"]
	errors := len(stats.Errors)
	iters := stats.Iterations

	// --- 1. Edit-to-iteration ratio ---
	// Healthy: at least 1 meaningful action per ~3 iterations for editing tasks.
	// Tasks with edits but low ratio indicate excessive exploration.
	if edits > 0 && iters > 5 {
		ratio := float64(edits) / float64(iters)
		if ratio < 0.15 {
			r.Score -= 25
			r.AntiPatterns = append(r.AntiPatterns,
				fmt.Sprintf("Low edit-to-iteration ratio (%d edits / %d iterations = %.2f)", edits, iters, ratio))
			r.Recommendations = append(r.Recommendations,
				"Plan file edits before starting — excessive iterations on few edits suggests exploration loops.")
		}
	}

	// --- 2. Read amplification ---
	// If reads >> unique files edited, the agent is re-reading or over-exploring.
	// Threshold: 8+ reads with 0 edits is pure exploration (could be research, so
	// only flag if there were also errors).
	if reads >= 8 && edits == 0 && errors > 0 {
		r.Score -= 15
		r.AntiPatterns = append(r.AntiPatterns,
			fmt.Sprintf("High read count (%d) with no edits and %d errors", reads, errors))
		r.Recommendations = append(r.Recommendations,
			"After reading, form a concrete plan before attempting edits to avoid trial-and-error cycles.")
	}

	// --- 3. Error rate ---
	// High error rate (>40% of tool calls erroring) signals a struggle.
	if totalCalls > 5 && errors > 0 {
		errRate := float64(errors) / float64(totalCalls) * 100
		if errRate > 40 {
			r.Score -= 20
			r.AntiPatterns = append(r.AntiPatterns,
				fmt.Sprintf("High tool error rate (%d/%d = %.0f%%)", errors, totalCalls, errRate))
			r.Recommendations = append(r.Recommendations,
				"Check file existence and read files before editing to reduce edit failures.")
		}
	}

	// --- 4. Context pressure ---
	// Approaching context limit degrades output quality.
	if stats.ContextWindow > 0 && stats.ContextPeakTokens > 0 {
		peakPct := float64(stats.ContextPeakTokens) / float64(stats.ContextWindow) * 100
		if peakPct > 85 {
			r.Score -= 15
			r.AntiPatterns = append(r.AntiPatterns,
				fmt.Sprintf("Context near capacity (%.0f%% of %dK)", peakPct, stats.ContextWindow/1000))
			r.Recommendations = append(r.Recommendations,
				"Batch reads and avoid re-reading — use targeted offset/limit reads for large files.")
		}
	}

	// --- 5. Compaction waste ---
	// Each compaction loses earlier context. Multiple compactions in one run
	// indicate the agent accumulated too much context before acting.
	if stats.CompactionCount >= 2 {
		r.Score -= 15
		r.AntiPatterns = append(r.AntiPatterns,
			fmt.Sprintf("%d context compaction events", stats.CompactionCount))
		r.Recommendations = append(r.Recommendations,
			"Act sooner after gathering information — compacting multiple times loses critical context.")
	}

	// Clamp score
	if r.Score < 0 {
		r.Score = 0
	}

	// Determine level
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
