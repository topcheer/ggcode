package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Plan Adherence Trend (r34): the plan-drift gate (plan_drift.go) internally
// computes an unaddressed-items ratio but collapses it into a fire-once
// binary warning, so gradual drift across a long run is invisible until the
// final reminder. This companion exposes that same coverage ratio as a
// continuous score (1.0 = every plan item backed by tool-facied evidence)
// sampled during execution, following 2026 observability practice of
// trace-level plan-adherence scoring (futureagi "LLM Agent Architectures
// 2026": span/trace/persona three-layer evals; trace level scores task
// completion, plan adherence, and budget).
//
// Zero LLM cost, deterministic; distinct from trajectory-confidence
// (confidence.go scores tool success rate, NOT plan coverage - its own
// comment in verification_debt.go concedes the plan dimension is untracked).

const (
	// planAdherenceSampleGap: minimum tool calls between recorded samples
	// (the score is recomputed cheaply; this throttles history + log noise).
	planAdherenceSampleGap = 5
	// planAdherenceMaxSamples caps history growth for very long runs.
	planAdherenceMaxSamples = 50
)

// planAdherenceSample is one recorded point of the adherence trend.
type planAdherenceSample struct {
	ToolCalls int     // cumulative tool-call count at sample time
	Score     float64 // 1 - unaddressed/total over captured plan items
}

// planAdherenceTrend tracks the adherence score across a run.
type planAdherenceTrend struct {
	toolCalls int                   // cumulative tool calls observed
	samples   []planAdherenceSample // recorded trend points
}

// noteToolCall samples plan adherence after a tool call, honoring the
// sample gap. Items come from the owning planDriftState; empty plans are a
// no-op (score is undefined without a plan).
func (t *planAdherenceTrend) noteToolCall(items []planItem, runStats *RunStats) {
	if t == nil || len(items) == 0 || runStats == nil {
		return
	}
	t.toolCalls++
	last := len(t.samples) - 1
	if last >= 0 && t.toolCalls-t.samples[last].ToolCalls < planAdherenceSampleGap {
		return
	}
	score := computePlanAdherence(items, runStats)
	if len(t.samples) >= planAdherenceMaxSamples {
		return
	}
	t.samples = append(t.samples, planAdherenceSample{ToolCalls: t.toolCalls, Score: score})
	prev := 0.0
	if last >= 0 {
		prev = t.samples[last].Score
	}
	debug.Log("agent", "plan adherence %.2f at tool call %d (prev %.2f)", score, t.toolCalls, prev)
}

// Samples returns the recorded trend points (oldest first).
func (t *planAdherenceTrend) Samples() []planAdherenceSample {
	if t == nil {
		return nil
	}
	return t.samples
}

// trendSummary renders a compact one-line trend for logs and future
// observability export, e.g. "plan adherence trend: 0.40 -> 0.60 (n=2)".
// Empty string when no samples were recorded.
func (t *planAdherenceTrend) trendSummary() string {
	if t == nil || len(t.samples) == 0 {
		return ""
	}
	first := t.samples[0].Score
	last := t.samples[len(t.samples)-1].Score
	return fmt.Sprintf("plan adherence trend: %.2f -> %.2f (n=%d, %d tool calls)",
		first, last, len(t.samples), t.toolCalls)
}

// computePlanAdherence returns the fraction of captured plan items whose
// keywords are covered by tool-faced work evidence (same coverage rule as
// checkPlanDrift: an item counts as addressed when at least half its
// keywords appear in the work corpus).
func computePlanAdherence(items []planItem, runStats *RunStats) float64 {
	if len(items) == 0 {
		return 1
	}
	corpus := buildPlanWorkCorpus(runStats)
	unaddressed := 0
	for _, item := range items {
		if len(item.Keywords) == 0 {
			continue
		}
		matched := 0
		for _, kw := range item.Keywords {
			if strings.Contains(corpus, kw) {
				matched++
			}
		}
		if matched < (len(item.Keywords)+1)/2 {
			unaddressed++
		}
	}
	return 1 - float64(unaddressed)/float64(len(items))
}
