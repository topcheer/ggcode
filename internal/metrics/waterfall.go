package metrics

import (
	"fmt"
	"sort"
	"time"
)

// WaterfallAnalysis provides latency breakdown for a single turn, identifying
// parallel vs sequential execution, the critical path, and bottleneck tools.
// This is the core observability primitive for "why was this turn slow?"
// debugging - equivalent to a flame graph for LLM agent execution.
type WaterfallAnalysis struct {
	TurnIndex            int           `json:"turn_index"`
	WallClock            time.Duration `json:"wall_clock_ms"`
	TotalToolTime        time.Duration `json:"total_tool_time_ms"`
	LLMTime              time.Duration `json:"llm_time_ms"`
	OverlapRatio         float64       `json:"overlap_ratio"`    // 0=fully sequential, 1=fully parallel
	CriticalPathDuration time.Duration `json:"critical_path_ms"` // longest sequential chain
	BottleneckTool       string        `json:"bottleneck_tool"`
	BottleneckDuration   time.Duration `json:"bottleneck_duration_ms"`
	ParallelToolGroups   [][]ToolSpan  `json:"parallel_groups,omitempty"`
	SequentialChain      []ToolSpan    `json:"sequential_chain,omitempty"`
}

// ToolSpan represents a single tool execution with reconstructed start/end times.
type ToolSpan struct {
	Name      string        `json:"name"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Duration  time.Duration `json:"duration_ms"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

// AnalyzeWaterfall produces a per-turn latency waterfall analysis from the
// flat MetricEvent list. For each turn it determines:
//
//   - Wall clock duration (earliest start to latest end)
//   - Total tool time (sum of all tool durations)
//   - Overlap ratio (how much tools ran in parallel)
//   - Critical path (longest sequential dependency chain)
//   - Bottleneck tool (slowest single tool)
//   - Parallel groups (sets of tools that overlapped in time)
//
// This is deterministic and requires no LLM calls - pure interval arithmetic.
func AnalyzeWaterfall(events []MetricEvent) []WaterfallAnalysis {
	if len(events) == 0 {
		return nil
	}

	// Group by turn.
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

// turnMetrics holds the intermediate data extracted from a turn's events.
type turnMetrics struct {
	toolSpans   []ToolSpan
	llmDuration time.Duration
	minStart    time.Time
	maxEnd      time.Time
}

// collectTurnMetrics iterates a turn's events and extracts tool spans,
// LLM duration, and time bounds.
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

// findBottleneck returns the name and duration of the slowest tool span.
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

// sumDurations returns the total duration across all tool spans.
func sumDurations(spans []ToolSpan) time.Duration {
	var total time.Duration
	for _, ts := range spans {
		total += ts.Duration
	}
	return total
}

// computeOverlapRatio measures how much tool execution overlapped.
// Returns 0 for fully sequential, approaching 1 for fully parallel.
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

	// Sort tool spans by start time.
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

// computeToolWallClock computes the actual wall-clock time covered by all
// tool spans (merging overlapping intervals), used to detect parallelism.
func computeToolWallClock(spans []ToolSpan) time.Duration {
	if len(spans) == 0 {
		return 0
	}

	// Sort by start time.
	sorted := make([]ToolSpan, len(spans))
	copy(sorted, spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartTime.Before(sorted[j].StartTime)
	})

	// Merge overlapping intervals.
	var merged []ToolSpan
	current := sorted[0]
	for i := 1; i < len(sorted); i++ {
		if !sorted[i].StartTime.After(current.EndTime) {
			// Overlaps - extend current.
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

// findParallelGroups identifies sets of tools that overlap in time.
// Tools whose [start, end) intervals intersect are grouped together.
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
			// Strictly overlaps (start < current end) with current group.
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

// findCriticalPath identifies the longest sequential chain of non-overlapping
// tool spans. This represents the minimum possible wall-clock time if tools
// were perfectly parallelized except for sequential dependencies.
//
// Algorithm: greedy interval scheduling - pick the earliest-ending non-overlapping
// span, then the next that starts after it ends, etc.
func findCriticalPath(spans []ToolSpan) []ToolSpan {
	if len(spans) == 0 {
		return nil
	}

	// Sort by end time (greedy earliest-finish-first).
	sorted := make([]ToolSpan, len(spans))
	copy(sorted, spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EndTime.Before(sorted[j].EndTime)
	})

	// Find the longest chain using dynamic programming on sorted intervals.
	// For each span, the longest chain ending at it = 1 + max(chain of all
	// spans ending before it starts).
	n := len(sorted)
	dp := make([]int, n)   // length of longest chain ending at i
	prev := make([]int, n) // previous index in chain (-1 if none)
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

	// Reconstruct chain.
	var chain []ToolSpan
	for idx := bestEnd; idx >= 0; {
		chain = append(chain, sorted[idx])
		if prev[idx] < 0 {
			break
		}
		idx = prev[idx]
	}

	// Reverse to chronological order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// FormatWaterfallSummary produces a human-readable summary string of the
// waterfall analysis for inline display in TUI or logs.
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
