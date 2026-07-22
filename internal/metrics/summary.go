package metrics

import (
	"math"
	"sort"
	"time"
)

type SessionSummary struct {
	TurnCount        int
	LLMCallCount     int
	ToolCallCount    int
	ToolFailureCount int

	TotalInputTokens  int
	TotalOutputTokens int
	TotalCacheRead    int
	TotalCacheWrite   int

	AvgTTFT     time.Duration
	P95TTFT     time.Duration
	AvgDuration time.Duration
	P95Duration time.Duration
	AvgThink    time.Duration

	SlowTools []ToolSummary
	Turns     []TurnSummary
}

type ToolSummary struct {
	Name        string
	Calls       int
	Failures    int
	AvgDuration time.Duration
	MaxDuration time.Duration
}

type TurnSummary struct {
	TurnIndex        int
	LLMCallCount     int
	ToolCallCount    int
	ToolFailureCount int
	TTFT             time.Duration
	Duration         time.Duration
	ThinkTime        time.Duration
	InputTokens      int
	OutputTokens     int
	CacheRead        int
	CacheWrite       int
	// Cumulative totals across all turns up to and including this one.
	CumInputTokens      int
	CumOutputTokens     int
	CumCacheRead        int
	CumCacheWrite       int
	SlowestTool         string
	SlowestToolDuration time.Duration
}

func (s SessionSummary) HasData() bool {
	return s.TurnCount > 0 || s.ToolCallCount > 0 || s.LLMCallCount > 0
}

func (s SessionSummary) ToolFailureRate() int {
	if s.ToolCallCount == 0 {
		return 0
	}
	return int(math.Round(float64(s.ToolFailureCount) * 100 / float64(s.ToolCallCount)))
}

func Summarize(events []MetricEvent) SessionSummary {
	if len(events) == 0 {
		return SessionSummary{}
	}

	type toolAggregate struct {
		calls       int
		failures    int
		total       time.Duration
		maxDuration time.Duration
	}

	turnsByIndex := make(map[int]*TurnSummary, len(events))
	toolsByName := make(map[string]*toolAggregate)

	for _, ev := range events {
		turn := turnsByIndex[ev.TurnIndex]
		if turn == nil {
			turn = &TurnSummary{TurnIndex: ev.TurnIndex}
			turnsByIndex[ev.TurnIndex] = turn
		}

		switch ev.Type {
		case "llm":
			turn.LLMCallCount++
			if ev.TTFT > 0 && (turn.TTFT == 0 || ev.TTFT < turn.TTFT) {
				turn.TTFT = ev.TTFT
			}
			turn.Duration += ev.Duration
			turn.ThinkTime += ev.ThinkTime
			turn.InputTokens += ev.InputTokens
			turn.OutputTokens += ev.OutputTokens
			turn.CacheRead += ev.CacheRead
			turn.CacheWrite += ev.CacheWrite
		case "tool":
			turn.ToolCallCount++
			if !ev.ToolSuccess || ev.ToolError != "" {
				turn.ToolFailureCount++
			}
			if ev.ToolDuration > turn.SlowestToolDuration {
				turn.SlowestToolDuration = ev.ToolDuration
				turn.SlowestTool = ev.ToolName
			}
			if ev.ToolName != "" {
				agg := toolsByName[ev.ToolName]
				if agg == nil {
					agg = &toolAggregate{}
					toolsByName[ev.ToolName] = agg
				}
				agg.calls++
				if !ev.ToolSuccess || ev.ToolError != "" {
					agg.failures++
				}
				agg.total += ev.ToolDuration
				if ev.ToolDuration > agg.maxDuration {
					agg.maxDuration = ev.ToolDuration
				}
			}
		}
	}

	out := SessionSummary{
		Turns: make([]TurnSummary, 0, len(turnsByIndex)),
	}
	ttfts := make([]time.Duration, 0, len(turnsByIndex))
	durations := make([]time.Duration, 0, len(turnsByIndex))
	thinks := make([]time.Duration, 0, len(turnsByIndex))

	for _, turn := range turnsByIndex {
		out.LLMCallCount += turn.LLMCallCount
		out.ToolCallCount += turn.ToolCallCount
		out.ToolFailureCount += turn.ToolFailureCount
		out.TotalInputTokens += turn.InputTokens
		out.TotalOutputTokens += turn.OutputTokens
		out.TotalCacheRead += turn.CacheRead
		out.TotalCacheWrite += turn.CacheWrite
		if turn.TTFT > 0 {
			ttfts = append(ttfts, turn.TTFT)
		}
		if turn.Duration > 0 {
			durations = append(durations, turn.Duration)
		}
		if turn.ThinkTime > 0 {
			thinks = append(thinks, turn.ThinkTime)
		}
		out.Turns = append(out.Turns, *turn)
	}

	sort.Slice(out.Turns, func(i, j int) bool {
		if out.Turns[i].TurnIndex == out.Turns[j].TurnIndex {
			return out.Turns[i].Duration > out.Turns[j].Duration
		}
		return out.Turns[i].TurnIndex < out.Turns[j].TurnIndex
	})
	// Fill cumulative token totals.
	var cumIn, cumOut, cumCR, cumCW int
	for i := range out.Turns {
		cumIn += out.Turns[i].InputTokens
		cumOut += out.Turns[i].OutputTokens
		cumCR += out.Turns[i].CacheRead
		cumCW += out.Turns[i].CacheWrite
		out.Turns[i].CumInputTokens = cumIn
		out.Turns[i].CumOutputTokens = cumOut
		out.Turns[i].CumCacheRead = cumCR
		out.Turns[i].CumCacheWrite = cumCW
	}
	out.TurnCount = len(out.Turns)
	out.AvgTTFT = averageDuration(ttfts)
	out.P95TTFT = percentileDuration(ttfts, 95)
	out.AvgDuration = averageDuration(durations)
	out.P95Duration = percentileDuration(durations, 95)
	out.AvgThink = averageDuration(thinks)

	out.SlowTools = make([]ToolSummary, 0, len(toolsByName))
	for name, agg := range toolsByName {
		avg := time.Duration(0)
		if agg.calls > 0 {
			avg = agg.total / time.Duration(agg.calls)
		}
		out.SlowTools = append(out.SlowTools, ToolSummary{
			Name:        name,
			Calls:       agg.calls,
			Failures:    agg.failures,
			AvgDuration: avg,
			MaxDuration: agg.maxDuration,
		})
	}
	sort.Slice(out.SlowTools, func(i, j int) bool {
		if out.SlowTools[i].AvgDuration == out.SlowTools[j].AvgDuration {
			if out.SlowTools[i].MaxDuration == out.SlowTools[j].MaxDuration {
				if out.SlowTools[i].Calls == out.SlowTools[j].Calls {
					return out.SlowTools[i].Name < out.SlowTools[j].Name
				}
				return out.SlowTools[i].Calls > out.SlowTools[j].Calls
			}
			return out.SlowTools[i].MaxDuration > out.SlowTools[j].MaxDuration
		}
		return out.SlowTools[i].AvgDuration > out.SlowTools[j].AvgDuration
	})

	return out
}

func TurnSummaryForIndex(events []MetricEvent, turnIndex int) (TurnSummary, bool) {
	if turnIndex <= 0 {
		return TurnSummary{}, false
	}
	summary := Summarize(events)
	for _, turn := range summary.Turns {
		if turn.TurnIndex == turnIndex {
			return turn, true
		}
	}
	return TurnSummary{}, false
}

func averageDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
}

func percentileDuration(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	cloned := append([]time.Duration(nil), values...)
	sort.Slice(cloned, func(i, j int) bool { return cloned[i] < cloned[j] })
	if percentile <= 0 {
		return cloned[0]
	}
	if percentile >= 100 {
		return cloned[len(cloned)-1]
	}
	index := int(math.Ceil(float64(percentile)*float64(len(cloned))/100.0)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(cloned) {
		index = len(cloned) - 1
	}
	return cloned[index]
}
