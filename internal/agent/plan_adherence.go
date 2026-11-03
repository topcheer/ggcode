package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	planAdherenceSampleGap  = 5
	planAdherenceMaxSamples = 50
)

type planAdherenceSample struct {
	ToolCalls int
	Score     float64
}

type planAdherenceTrend struct {
	toolCalls int
	samples   []planAdherenceSample
}

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

func (t *planAdherenceTrend) Samples() []planAdherenceSample {
	if t == nil {
		return nil
	}
	return t.samples
}

func (t *planAdherenceTrend) trendSummary() string {
	if t == nil || len(t.samples) == 0 {
		return ""
	}
	first := t.samples[0].Score
	last := t.samples[len(t.samples)-1].Score
	return fmt.Sprintf("plan adherence trend: %.2f -> %.2f (n=%d, %d tool calls)",
		first, last, len(t.samples), t.toolCalls)
}

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
