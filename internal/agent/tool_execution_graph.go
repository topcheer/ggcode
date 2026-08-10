package agent

import (
	"sync"
	"time"
)

// toolExecutionGraph tracks tool execution outcomes and builds
// a dynamic dependency graph based on actual execution patterns.
// Research basis: NaviAgent (arxiv:2506.19500) - execution feedback
// continually updates tool composition graphs for adaptive orchestration.
type toolExecutionGraph struct {
	mu sync.RWMutex

	// Success rates per tool
	successRates map[string]*toolStats

	// Tool composition patterns: which tools work well together
	composition map[string]map[string]*patternScore // toolA -> toolB -> score

	// Recent execution history (circular buffer, max 100 entries)
	history    []*executionRecord
	historyIdx int
}

type toolStats struct {
	successes  int
	failures   int
	lastUsed   time.Time
	avgLatency time.Duration
}

type patternScore struct {
	count       int
	successRate float64
	lastSeen    time.Time
}

type executionRecord struct {
	tool      string
	success   bool
	latency   time.Duration
	timestamp time.Time
	context   string // preceding tool name, if any
}

var (
	// Global instance
	graphInstance *toolExecutionGraph
	once          sync.Once
)

// getToolExecutionGraph returns the singleton instance
func getToolExecutionGraph() *toolExecutionGraph {
	once.Do(func() {
		graphInstance = &toolExecutionGraph{
			successRates: make(map[string]*toolStats),
			composition:  make(map[string]map[string]*patternScore),
			history:      make([]*executionRecord, 100),
		}
	})
	return graphInstance
}

// recordToolOutcome records a tool execution result
func (g *toolExecutionGraph) recordToolOutcome(tool string, success bool, latency time.Duration, precedingTool string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Update tool stats
	stats := g.successRates[tool]
	if stats == nil {
		stats = &toolStats{}
		g.successRates[tool] = stats
	}
	if success {
		stats.successes++
	} else {
		stats.failures++
	}
	stats.lastUsed = time.Now()
	// Exponential moving average for latency
	if stats.avgLatency == 0 {
		stats.avgLatency = latency
	} else {
		stats.avgLatency = (stats.avgLatency*9 + latency) / 10
	}

	// Update composition pattern if we have context
	if precedingTool != "" {
		if g.composition[precedingTool] == nil {
			g.composition[precedingTool] = make(map[string]*patternScore)
		}
		pattern := g.composition[precedingTool][tool]
		if pattern == nil {
			pattern = &patternScore{}
			g.composition[precedingTool][tool] = pattern
		}
		pattern.count++
		pattern.lastSeen = time.Now()
		// Update success rate
		total := pattern.count
		if success {
			pattern.successRate = (pattern.successRate*float64(total-1) + 1.0) / float64(total)
		} else {
			pattern.successRate = (pattern.successRate * float64(total-1)) / float64(total)
		}
	}

	// Record in history buffer
	record := &executionRecord{
		tool:      tool,
		success:   success,
		latency:   latency,
		timestamp: time.Now(),
		context:   precedingTool,
	}
	g.history[g.historyIdx] = record
	g.historyIdx = (g.historyIdx + 1) % len(g.history)
}

// getToolSuccessRate returns the success rate (0-1) for a tool
func (g *toolExecutionGraph) getToolSuccessRate(tool string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := g.successRates[tool]
	if stats == nil || stats.successes+stats.failures == 0 {
		return 0.5 // neutral for unseen tools
	}
	return float64(stats.successes) / float64(stats.successes+stats.failures)
}

// getPatternScore returns the composition score (0-1) for toolA followed by toolB
func (g *toolExecutionGraph) getPatternScore(toolA, toolB string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.composition[toolA] == nil {
		return 0.5 // neutral for unseen patterns
	}
	pattern := g.composition[toolA][toolB]
	if pattern == nil || pattern.count < 3 {
		return 0.5 // insufficient data
	}
	return pattern.successRate
}

// getLastUsed returns the last usage time for a tool
func (g *toolExecutionGraph) getLastUsed(tool string) (time.Time, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := g.successRates[tool]
	if stats == nil {
		return time.Time{}, false
	}
	return stats.lastUsed, true
}

// getRecentFailures returns count of recent failures for a tool (within last 5 minutes)
func (g *toolExecutionGraph) getRecentFailures(tool string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	now := time.Now()
	fiveMinAgo := now.Add(-5 * time.Minute)
	count := 0
	for i := 0; i < len(g.history); i++ {
		record := g.history[i]
		if record == nil {
			continue
		}
		if record.tool == tool && !record.success && record.timestamp.After(fiveMinAgo) {
			count++
		}
	}
	return count
}
