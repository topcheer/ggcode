// Per-axis trajectory scoring for runeval reports.
//
// Frontier agent-evaluation practice in 2025-26 converged on one lesson:
// a single aggregate score hides which dimension regressed ("an aggregate
// 0.85 hides a 0.62 on argument extraction behind a 0.97 on tool
// selection"). The 2026 methodology formalizes this as six evaluation
// dimensions scored independently with per-dimension gates, plus a 4-D
// trajectory score whose purpose is diagnosis: when the composite drops,
// the axes are the diagnosis — the failing axis name is the root cause.
//
// ggcode's runeval had exactly the aggregate-only shape: one
// EfficiencyScore blending error rate, wasted repeats, and overhead share.
// This file decomposes the scorecard into named axes computed from the
// same deterministic walk — zero extra LLM cost, same transparency rules
// (each axis states its penalty and the numbers behind it). Only signals
// an offline, judge-free evaluator can honestly measure are used:
//
//   - error-recovery:     tool error rate + identical-input retries after
//     failures (the "retry the same broken input" anti-pattern)
//   - result-utilization: identical re-reads on read-only tools —
//     information re-fetched when already in context (deterministic
//     proxy for result-utilization / grounding failures)
//   - loop-discipline:    identical re-invocations of stateful tools —
//     repeated work that returned the same bytes every time (deterministic
//     proxy for plan-loop failures)
//   - context-economy:    non-agent machinery share + wasted context tax
//     (tokens that bought nothing)
//
// Axis scores are advisory only — runeval never steers the live loop.
package runeval

import (
	"fmt"
	"sort"
)

// weakestAxisFindingThreshold is the score below which the weakest axis
// earns a findings entry and the diagnosis line in Render.
const weakestAxisFindingThreshold = 80

// AxisScore is one named, independently-scored dimension of a trajectory.
// Name is a stable kebab-case identifier suitable for diffing across runs.
type AxisScore struct {
	Name   string
	Score  int    // 0-100
	Detail string // quantified diagnosis; empty when nothing dragged the score
}

// computeAxes derives the axis scores from a Report's counters and returns
// them worst-first. Pure: same report in, same axes out.
func computeAxes(r Report) []AxisScore {
	axes := []AxisScore{
		errorRecoveryAxis(r),
		resultUtilizationAxis(r),
		loopDisciplineAxis(r),
		contextEconomyAxis(r),
	}
	sort.SliceStable(axes, func(i, j int) bool { return axes[i].Score < axes[j].Score })
	return axes
}

// pctClamped rounds a 0-100 point value, clamped to [0,100].
func pctClamped(points float64) int {
	if points < 0 {
		points = 0
	}
	if points > 100 {
		points = 100
	}
	return int(points + 0.5)
}

func errorRecoveryAxis(r Report) AxisScore {
	a := AxisScore{Name: "error-recovery", Score: 100}
	if r.ToolCalls == 0 {
		return a
	}
	errRate := float64(r.ToolErrors) / float64(r.ToolCalls)
	// Every counted ErrorRetries is, by construction, a failure whose
	// exact canonical input was re-sent later in the trajectory.
	retryRate := float64(r.ErrorRetries) / float64(r.ToolCalls)
	// Weighting: an unresolved failure class costs more than one extra
	// retry — 70/30 mirrors the composite's view that errors are the most
	// damaging signal, while still exposing the retry anti-pattern.
	a.Score = pctClamped(100 - 70*errRate - 30*retryRate)
	if r.ToolErrors > 0 || r.ErrorRetries > 0 {
		a.Detail = fmt.Sprintf("%d/%d calls failed", r.ToolErrors, r.ToolCalls)
		if r.ErrorRetries > 0 {
			a.Detail += fmt.Sprintf(" · %d identical-input retry after failure", r.ErrorRetries)
		}
	}
	return a
}

func resultUtilizationAxis(r Report) AxisScore {
	a := AxisScore{Name: "result-utilization", Score: 100}
	if r.ToolCalls == 0 {
		return a
	}
	if r.ReadOnlyRepeats > 0 {
		penalty := 100 * float64(r.ReadOnlyRepeats) / float64(r.ToolCalls)
		a.Score = pctClamped(100 - penalty)
		a.Detail = fmt.Sprintf("%d identical re-reads fetched data already in context", r.ReadOnlyRepeats)
	}
	return a
}

func loopDisciplineAxis(r Report) AxisScore {
	a := AxisScore{Name: "loop-discipline", Score: 100}
	if r.ToolCalls == 0 {
		return a
	}
	if r.StatefulLoopRepeats > 0 {
		penalty := 100 * float64(r.StatefulLoopRepeats) / float64(r.ToolCalls)
		a.Score = pctClamped(100 - penalty)
		a.Detail = fmt.Sprintf("%d identical stateful re-invocations returned the same output — possible loop", r.StatefulLoopRepeats)
	}
	return a
}

func contextEconomyAxis(r Report) AxisScore {
	a := AxisScore{Name: "context-economy", Score: 100}
	if r.TotalTokens <= 0 {
		return a
	}
	overheadShare := 0.0
	if r.ToolCalls >= minToolsForOverheadPenalty {
		overheadShare = float64(r.OverheadTokens) / float64(r.TotalTokens)
	}
	taxShare := float64(r.WastedTokenEstimate) / float64(r.TotalTokens)
	if overheadShare > 0 || taxShare > 0 {
		// Equal weights: overhead machinery and wasted tax are both
		// "tokens that bought nothing", just via different mechanisms.
		a.Score = pctClamped(100 - 50*overheadShare - 50*taxShare)
		a.Detail = fmt.Sprintf("%.0f%% overhead machinery · %.0f%% wasted context tax", 100*overheadShare, 100*taxShare)
	}
	return a
}
