package agent

// Context Window Pressure Forecaster - Context Engineering Intelligence
//
// Research basis: Anthropic's Context Engineering guide (2025) and the ACE
// benchmark (ICLR 2026) emphasize proactive context management over reactive
// compaction. Reactive compaction (triggered at X% of window) has a critical
// drawback: it fires DURING an active turn, abruptly truncating conversation
// history at an unpredictable point. This can lose mid-task context (e.g.,
// the agent was about to reference a tool result from 3 turns ago that gets
// compacted away).
//
// Competitor approaches:
//   - Claude Code: auto-compact at ~95% capacity (purely reactive)
//   - Cursor: implicit context management via editor state
//   - Aider: user-managed context, manual /clear
//   - OpenHands: reactive compaction with LLM summarization
//
// What this forecaster does:
//   - Tracks token consumption rate per iteration (delta between calls)
//   - Estimates remaining iterations before the compaction threshold is hit
//   - When estimated iterations-to-compaction falls below a warning threshold,
//     injects guidance so the agent can proactively:
//     * Wrap up the current task concisely
//     * Avoid starting large new sub-tasks
//     * Summarize key findings before forced compaction
//     * Consider using more targeted tool calls (offset/limit) to slow growth
//
// This is different from:
//   - budget.go (context budget): analyzes WHAT is consuming context (breakdown)
//   - context_footprint.go: identifies WHICH tools dominate context (attribution)
//   - agent_precompact.go: performs background compaction (execution)
//   - This forecaster: predicts WHEN compaction will be needed (timing)
//
// Zero LLM cost - deterministic token arithmetic + linear projection.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// pressureWindow: number of recent samples for trend analysis.
	pressureWindow = 6

	// pressureMinSamples: minimum data points before forecasting.
	pressureMinSamples = 3

	// pressureWarnIterations: warn when fewer than this many iterations remain
	// before compaction threshold. Gives the agent time to wrap up.
	pressureWarnIterations = 4

	// pressureCriticalIterations: urgent warning level.
	pressureCriticalIterations = 2

	// pressureCompactionThreshold: the fraction of context window at which
	// reactive compaction typically triggers (matches ContextManager default).
	pressureCompactionThreshold = 0.80

	// pressureCooldown prevents repeated warnings within this window.
	pressureCooldown = 3 * time.Minute

	// pressureMaxWarnings caps total warnings per run.
	pressureMaxWarnings = 3
)

// pressureSample records token usage at a point in the conversation.
type pressureSample struct {
	iteration   int // agent loop iteration number
	totalTokens int // total context tokens at this point (input + cacheRead)
	timestamp   time.Time
}

// pressureForecaster predicts when the context window will hit the compaction
// threshold, allowing the agent to proactively manage context.
type pressureForecaster struct {
	mu sync.Mutex

	samples       []pressureSample
	contextWindow int // max context window for the model (0 = unknown)
	lastWarnAt    time.Time
	warningCount  int
	lastWarnLevel string // "warn" or "critical" - avoids duplicate same-level warnings
}

func newPressureForecaster() *pressureForecaster {
	return &pressureForecaster{
		samples: make([]pressureSample, 0, pressureWindow+1),
	}
}

// reset clears state for a new run.
func (f *pressureForecaster) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = f.samples[:0]
	f.lastWarnAt = time.Time{}
	f.warningCount = 0
	f.lastWarnLevel = ""
}

// setContextWindow updates the model's context window size.
func (f *pressureForecaster) setContextWindow(window int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contextWindow = window
}

// record tracks a new LLM call's token usage and returns guidance if pressure
// is building toward compaction.
func (f *pressureForecaster) record(iteration int, usage provider.TokenUsage) string {
	totalTokens := usage.InputTokens + usage.CacheRead

	f.mu.Lock()
	defer f.mu.Unlock()

	sample := pressureSample{
		iteration:   iteration,
		totalTokens: totalTokens,
		timestamp:   time.Now(),
	}
	f.samples = append(f.samples, sample)
	if len(f.samples) > pressureWindow {
		f.samples = f.samples[1:]
	}

	if f.contextWindow <= 0 {
		return "" // can't forecast without knowing window size
	}

	if len(f.samples) < pressureMinSamples {
		return ""
	}

	// Cooldown check
	if time.Since(f.lastWarnAt) < pressureCooldown && f.warningCount > 0 {
		return ""
	}

	if f.warningCount >= pressureMaxWarnings {
		return ""
	}

	compactionThreshold := int(float64(f.contextWindow) * pressureCompactionThreshold)
	current := f.samples[len(f.samples)-1].totalTokens

	if current >= compactionThreshold {
		return "" // already at/over threshold - compaction will handle it
	}

	remaining := compactionThreshold - current
	if remaining <= 0 {
		return ""
	}

	// Estimate growth rate (tokens per iteration) using linear regression
	growthRate := f.estimateGrowthRate()
	if growthRate <= 0 {
		return "" // context isn't growing (compacted or stable)
	}

	itersToCompaction := int(float64(remaining) / growthRate)
	if itersToCompaction < 0 {
		return "" // overflow or negative, shouldn't happen
	}

	level := ""
	if itersToCompaction <= pressureCriticalIterations {
		level = "critical"
	} else if itersToCompaction <= pressureWarnIterations {
		level = "warn"
	}

	if level == "" {
		return ""
	}

	// Skip if same level was already warned (avoids repeating "warn" every iter)
	if level == f.lastWarnLevel && f.warningCount > 0 && time.Since(f.lastWarnAt) < pressureCooldown {
		return ""
	}

	// Allow critical to upgrade from warn
	if level == "warn" && f.lastWarnLevel == "critical" {
		return "" // already escalated past this
	}

	f.lastWarnAt = time.Now()
	f.warningCount++
	f.lastWarnLevel = level

	guidance := f.formatGuidance(level, current, compactionThreshold, growthRate, itersToCompaction)
	debug.Log("context-pressure", "%s: %d/%d tokens, %.0f tok/iter, ~%d iters to compaction",
		level, current, compactionThreshold, growthRate, itersToCompaction)
	return guidance
}

// estimateGrowthRate computes the average token growth per iteration using
// the recent sample window. Uses simple linear regression (least squares).
func (f *pressureForecaster) estimateGrowthRate() float64 {
	n := len(f.samples)
	if n < 2 {
		return 0
	}

	// Use least squares: slope = (n*sum(xy) - sum(x)*sum(y)) / (n*sum(x^2) - sum(x)^2)
	var sumX, sumY, sumXY, sumX2 float64
	for _, s := range f.samples {
		x := float64(s.iteration)
		y := float64(s.totalTokens)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	nf := float64(n)
	denominator := nf*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}

	slope := (nf*sumXY - sumX*sumY) / denominator
	return slope
}

// formatGuidance produces actionable guidance for the predicted pressure level.
func (f *pressureForecaster) formatGuidance(level string, current, threshold int, growthRate float64, itersToCompaction int) string {
	var sb strings.Builder

	if level == "critical" {
		sb.WriteString("[Context Pressure: CRITICAL] ")
	} else {
		sb.WriteString("[Context Pressure: WARNING] ")
	}

	usedPct := float64(current) / float64(f.contextWindow) * 100
	sb.WriteString(fmt.Sprintf("Context window is %.0f%% full (%d/%d tokens). ", usedPct, current, f.contextWindow))
	sb.WriteString(fmt.Sprintf("At current growth rate (~%.0f tokens/iteration), ", growthRate))
	sb.WriteString(fmt.Sprintf("forced compaction will trigger in ~%d iterations.\n\n", itersToCompaction))

	sb.WriteString("Proactive recommendations:\n")
	sb.WriteString("  - Wrap up the current sub-task concisely rather than starting new exploratory searches\n")
	sb.WriteString("  - Use targeted tool calls (offset/limit, narrower grep patterns) to minimize context growth\n")
	sb.WriteString("  - Summarize key findings in your next response so they survive potential compaction\n")
	if level == "critical" {
		sb.WriteString("  - CRITICAL: Avoid multi-file reads or broad searches - each will accelerate compaction\n")
	}

	return sb.String()
}
