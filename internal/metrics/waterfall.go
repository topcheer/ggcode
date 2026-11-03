package metrics

import (
	"fmt"
	"sort"
	"time"
)

type WaterfallAnalysis struct {
	TurnIndex            int           `json:"turn_index"`
	WallClock            time.Duration `json:"wall_clock_ms"`
	TotalToolTime        time.Duration `json:"total_tool_time_ms"`
	LLMTime              time.Duration `json:"llm_time_ms"`
	OverlapRatio         float64       `json:"overlap_ratio"`
	CriticalPathDuration time.Duration `json:"critical_path_ms"`
	BottleneckTool       string        `json:"bottleneck_tool"`
	BottleneckDuration   time.Duration `json:"bottleneck_duration_ms"`
	ParallelToolGroups   [][]ToolSpan  `json:"parallel_groups,omitempty"`
	SequentialChain      []ToolSpan    `json:"sequential_chain,omitempty"`
}

type ToolSpan struct {
	Name      string        `json:"name"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Duration  time.Duration `json:"duration_ms"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

func AnalyzeWaterfall(events []MetricEvent) []WaterfallAnalysis {
	if len(events) == 0 {
		return nil
	}

	turnEvents := make(map[int][]MetricEvent)
	for _, ev := range events {
		turnEvents[ev.TurnIndex] = append(turnEvents[ev.TurnIndex], ev)
	}

	turns := make([]int, 0, len(turnEvents))
	for idx := range turnEvents {
		turns = append(turns, idx)
	}
	sort.Ints(turns)

	results := make([]WaterfallAnalysis, 0, len(turns))
	for _, turnIdx := range turns {
		results = append(results, analyzeTurn(turnIdx, turnEvents[turnIdx]))
	}
	return results
}

type turnMetrics struct {
	toolSpans   []ToolSpan
	llmDuration time.Duration
	minStart    time.Time
	maxEnd      time.Time
}

func collectTurnMetrics(events []MetricEvent) turnMetrics {
	var m turnMetrics
	for _, ev := range events {
		start := eventStart(ev)
		end := ev.Timestamp
		if end.IsZero() {
			end = start
		}
		if !start.IsZero() && (m.minStart.IsZero() || start.Before(m.minStart)) {
			m.minStart = start
		}
		if !end.IsZero() && (m.maxEnd.IsZero() || end.After(m.maxEnd)) {
			m.maxEnd = end
		}
		switch ev.Type {
		case "llm":
			m.llmDuration += ev.Duration
		case "tool":
			m.toolSpans = append(m.toolSpans, ToolSpan{
				Name:      ev.ToolName,
				StartTime: start,
				EndTime:   end,
				Duration:  ev.ToolDuration,
				Success:   ev.ToolSuccess,
				Error:     ev.ToolError,
			})
		}
	}
	return m
}

func findBottleneck(spans []ToolSpan) (string, time.Duration) {
	var name string
	var dur time.Duration
	for _, ts := range spans {
		if ts.Duration > dur {
			dur = ts.Duration
			name = ts.Name
		}
	}
	return name, dur
}

func sumDurations(spans []ToolSpan) time.Duration {
	var total time.Duration
	for _, ts := range spans {
		total += ts.Duration
	}
	return total
}

func computeOverlapRatio(spans []ToolSpan, totalToolTime, wallClock time.Duration) float64 {
	if totalToolTime <= 0 || wallClock <= 0 {
		return 0
	}
	toolWallClock := computeToolWallClock(spans)
	if totalToolTime <= toolWallClock {
		return 0
	}
	return float64(totalToolTime-toolWallClock) / float64(totalToolTime)
}

func analyzeTurn(turnIdx int, events []MetricEvent) WaterfallAnalysis {
	m := collectTurnMetrics(events)

	sort.SliceStable(m.toolSpans, func(i, j int) bool {
		return m.toolSpans[i].StartTime.Before(m.toolSpans[j].StartTime)
	})

	totalToolTime := sumDurations(m.toolSpans)
	bottleneckName, bottleneckDur := findBottleneck(m.toolSpans)

	wallClock := time.Duration(0)
	if !m.minStart.IsZero() && !m.maxEnd.IsZero() {
		wallClock = m.maxEnd.Sub(m.minStart)
	}

	overlapRatio := computeOverlapRatio(m.toolSpans, totalToolTime, wallClock)

	parallelGroups := findParallelGroups(m.toolSpans)
	seqChain := findCriticalPath(m.toolSpans)
	criticalPath := sumDurations(seqChain)

	return WaterfallAnalysis{
		TurnIndex:            turnIdx,
		WallClock:            wallClock,
		TotalToolTime:        totalToolTime,
		LLMTime:              m.llmDuration,
		OverlapRatio:         overlapRatio,
		CriticalPathDuration: criticalPath,
		BottleneckTool:       bottleneckName,
		BottleneckDuration:   bottleneckDur,
		ParallelToolGroups:   parallelGroups,
		SequentialChain:      seqChain,
	}
}

func computeToolWallClock(spans []ToolSpan) time.Duration {
	if len(spans) == 0 {
		return 0
	}

	sorted := make([]ToolSpan, len(spans))
	copy(sorted, spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartTime.Before(sorted[j].StartTime)
	})

	var merged []ToolSpan
	current := sorted[0]
	for i := 1; i < len(sorted); i++ {
		if !sorted[i].StartTime.After(current.EndTime) {

			if sorted[i].EndTime.After(current.EndTime) {
				current.EndTime = sorted[i].EndTime
			}
		} else {
			merged = append(merged, current)
			current = sorted[i]
		}
	}
	merged = append(merged, current)

	var total time.Duration
	for _, m := range merged {
		total += m.EndTime.Sub(m.StartTime)
	}
	return total
}

func findParallelGroups(spans []ToolSpan) [][]ToolSpan {
	if len(spans) <= 1 {
		return nil
	}

	sorted := make([]ToolSpan, len(spans))
	copy(sorted, spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartTime.Before(sorted[j].StartTime)
	})

	var groups [][]ToolSpan
	currentGroup := []ToolSpan{sorted[0]}
	currentEnd := sorted[0].EndTime

	for i := 1; i < len(sorted); i++ {
		if sorted[i].StartTime.Before(currentEnd) {

			currentGroup = append(currentGroup, sorted[i])
			if sorted[i].EndTime.After(currentEnd) {
				currentEnd = sorted[i].EndTime
			}
		} else {
			if len(currentGroup) > 1 {
				groups = append(groups, currentGroup)
			}
			currentGroup = []ToolSpan{sorted[i]}
			currentEnd = sorted[i].EndTime
		}
	}
	if len(currentGroup) > 1 {
		groups = append(groups, currentGroup)
	}
	return groups
}

func findCriticalPath(spans []ToolSpan) []ToolSpan {
	if len(spans) == 0 {
		return nil
	}

	sorted := make([]ToolSpan, len(spans))
	copy(sorted, spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EndTime.Before(sorted[j].EndTime)
	})

	n := len(sorted)
	dp := make([]int, n)
	prev := make([]int, n)
	for i := range dp {
		dp[i] = int(sorted[i].Duration / time.Millisecond)
		prev[i] = -1
	}

	bestEnd := 0
	for i := 1; i < n; i++ {
		for j := 0; j < i; j++ {
			if !sorted[j].EndTime.After(sorted[i].StartTime) {
				chainLen := dp[j] + int(sorted[i].Duration/time.Millisecond)
				if chainLen > dp[i] {
					dp[i] = chainLen
					prev[i] = j
				}
			}
		}
		if dp[i] > dp[bestEnd] {
			bestEnd = i
		}
	}

	var chain []ToolSpan
	for idx := bestEnd; idx >= 0; {
		chain = append(chain, sorted[idx])
		if prev[idx] < 0 {
			break
		}
		idx = prev[idx]
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func FormatWaterfallSummary(turns []WaterfallAnalysis) string {
	if len(turns) == 0 {
		return ""
	}
	var sb []byte
	for _, t := range turns {
		sb = append(sb, fmt.Sprintf("Turn %d: wall=%s tools=%s llm=%s overlap=%.0f%% critical=%s",
			t.TurnIndex,
			t.WallClock.Round(time.Millisecond),
			t.TotalToolTime.Round(time.Millisecond),
			t.LLMTime.Round(time.Millisecond),
			t.OverlapRatio*100,
			t.CriticalPathDuration.Round(time.Millisecond))...)
		if t.BottleneckTool != "" {
			sb = append(sb, fmt.Sprintf(" bottleneck=%s(%s)", t.BottleneckTool,
				t.BottleneckDuration.Round(time.Millisecond))...)
		}
		if len(t.ParallelToolGroups) > 0 {
			sb = append(sb, fmt.Sprintf(" parallel_groups=%d", len(t.ParallelToolGroups))...)
		}
		sb = append(sb, '\n')
	}
	return string(sb)
}
