package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// latencyMonitoredTools lists tools where slow execution is abnormal and
// indicates a potential problem (e.g., reading a huge file, overly broad
// search). Tools that are legitimately slow (run_command, builds, browser)
// are excluded so the warning is meaningful when it fires.
var latencyMonitoredTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"search_files":            true,
	"grep":                    true,
	"glob":                    true,
	"list_directory":          true,
	"edit_file":               true,
	"multi_edit_file":         true,
	"multi_file_edit":         true,
	"lsp_symbols":             true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_hover":               true,
	"lsp_workspace_symbols":   true,
	"lsp_document_highlights": true,
	"lsp_implementation":      true,
	"lsp_diagnostics":         true,
	"code_search":             true,
}

// latencyMinSamples is the minimum number of recorded samples before outlier
// detection activates. With fewer samples the baseline is unreliable.
const latencyMinSamples = 3

// latencyOutlierMultiplier flags a tool call as an outlier when its duration
// exceeds this multiple of the rolling mean.
const latencyOutlierMultiplier = 5.0

// latencyAbsoluteFloor is the minimum duration below which we never warn,
// even if statistically anomalous. Sub-second calls are never "slow enough"
// to warrant a context-consuming warning.
const latencyAbsoluteFloor = 2 * time.Second

// latencyWarnCooldown prevents the same tool from being warned about more
// than once per cooldown window, avoiding noise when a tool is consistently
// slow (e.g., reading from a network mount).
const latencyWarnCooldown = 5 * time.Minute

// latencySample is a single recorded duration for a tool.
type latencySample struct {
	dur      time.Duration
	recorded time.Time
}

// LatencyTracker maintains per-tool rolling latency statistics and detects
// statistical outliers — tool calls that are dramatically slower than the
// tool's established baseline. When an outlier is detected, a concise
// performance hint is generated so the agent can self-optimize (e.g., use
// offset/limit on reads, narrow search patterns).
//
// Research basis (2025-2026): Tool success rate is a critical indicator for
// tool selection. Studies show that tools with low historical success rates
// (e.g., unstable MCP servers, flaky web fetches) should be deprioritized
// or retried with fallback strategies. This extension adds success rate
// tracking alongside latency tracking.
type LatencyTracker struct {
	mu       sync.Mutex
	samples  map[string][]latencySample // tool name → recent durations
	lastWarn map[string]time.Time       // tool name → last warning time (cooldown)
	success  map[string]int             // tool name → successful calls count
	failure  map[string]int             // tool name → failed calls count
}

// NewLatencyTracker creates a ready-to-use LatencyTracker.
func NewLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		samples:  make(map[string][]latencySample),
		lastWarn: make(map[string]time.Time),
		success:  make(map[string]int),
		failure:  make(map[string]int),
	}
}

// maxLatencySamples caps the rolling window per tool to bound memory.
const maxLatencySamples = 20

// RecordAndCheck records a tool execution duration and returns a non-empty
// advisory string if this call is a statistical outlier relative to the
// tool's baseline. Returns "" when no warning is warranted.
//
// Latency is recorded for ALL tools (not just latencyMonitoredTools) so the
// adaptive timeout system (adaptive_timeout.go) can compute per-tool timeouts.
// The outlier warning, however, is only generated for monitored tools.
func (lt *LatencyTracker) RecordAndCheck(toolName string, dur time.Duration) string {
	if lt == nil {
		return ""
	}

	lt.mu.Lock()
	defer lt.mu.Unlock()

	samples := lt.samples[toolName]

	// Compute warning BEFORE adding the current sample so the outlier is
	// measured against the prior baseline (otherwise an extreme value
	// inflates the mean and masks itself). Only for monitored tools.
	var warning string
	if latencyMonitoredTools[toolName] {
		warning = lt.checkOutlier(toolName, dur, samples)
	}

	// Record latency for ALL tools (used by adaptive timeout computation).
	samples = append(samples, latencySample{dur: dur, recorded: time.Now()})
	if len(samples) > maxLatencySamples {
		samples = samples[len(samples)-maxLatencySamples:]
	}
	lt.samples[toolName] = samples

	return warning
}

// checkOutlier determines whether dur is an outlier relative to the existing
// sample baseline. Caller must hold lt.mu.
func (lt *LatencyTracker) checkOutlier(toolName string, dur time.Duration, samples []latencySample) string {
	// Not enough baseline data.
	if len(samples) < latencyMinSamples {
		return ""
	}

	// Below absolute floor — never warn for fast operations.
	if dur < latencyAbsoluteFloor {
		return ""
	}

	// Compute mean of existing samples.
	var total time.Duration
	for _, s := range samples {
		total += s.dur
	}
	mean := total / time.Duration(len(samples))
	if mean <= 0 {
		return ""
	}

	// Only warn when dramatically slower than baseline.
	if float64(dur) < float64(mean)*latencyOutlierMultiplier {
		return ""
	}

	// Cooldown: don't warn about the same tool repeatedly.
	if last, ok := lt.lastWarn[toolName]; ok {
		if time.Since(last) < latencyWarnCooldown {
			return ""
		}
	}
	lt.lastWarn[toolName] = time.Now()

	return formatLatencyWarning(toolName, dur, mean)
}

// formatLatencyWarning builds a concise, actionable hint for the agent.
func formatLatencyWarning(toolName string, dur, mean time.Duration) string {
	ratio := float64(dur) / float64(mean)
	if ratio < 1 {
		ratio = 1
	}

	var hint string
	switch {
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "file"):
		hint = "consider using offset/limit to read only the relevant section."
	case strings.Contains(toolName, "search") || strings.Contains(toolName, "grep") || toolName == "glob":
		hint = "consider narrowing the search pattern or directory scope."
	case strings.Contains(toolName, "edit"):
		hint = "this should normally be fast — check if the file is unusually large."
	case strings.Contains(toolName, "lsp"):
		hint = "LSP may be indexing — subsequent calls should be faster."
	default:
		hint = "consider narrowing the operation scope."
	}

	return fmt.Sprintf(
		"Performance note: %s took %.1fs — %.0fx slower than its average (%.2fs). %s",
		toolName, dur.Seconds(), ratio, mean.Seconds(), hint,
	)
}

// meanLatency returns the rolling mean duration for a tool, or 0 if no data.
func (lt *LatencyTracker) meanLatency(toolName string) time.Duration {
	if lt == nil {
		return 0
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	samples := lt.samples[toolName]
	if len(samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, s := range samples {
		total += s.dur
	}
	return total / time.Duration(len(samples))
}

// sampleCount returns the number of latency samples recorded for a tool.
// Adaptive-timeout tightening is gated on this (#366): a single fast
// sample (e.g. one cache-hit grep at 50ms) used to clamp the timeout to
// the 10s floor and kill legitimate 25s searches on the very next call.
func (lt *LatencyTracker) sampleCount(toolName string) int {
	if lt == nil {
		return 0
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return len(lt.samples[toolName])
}

// RecordSuccess records a successful tool execution for success rate tracking.
func (lt *LatencyTracker) RecordSuccess(toolName string) {
	if lt == nil {
		return
	}
	lt.mu.Lock()
	lt.success[toolName]++
	lt.mu.Unlock()
}

// RecordFailure records a failed tool execution for success rate tracking.
func (lt *LatencyTracker) RecordFailure(toolName string) {
	if lt == nil {
		return
	}
	lt.mu.Lock()
	lt.failure[toolName]++
	lt.mu.Unlock()
}

// SuccessRate returns the success rate (0.0-1.0) for a tool, or 0 if no data.
func (lt *LatencyTracker) SuccessRate(toolName string) float64 {
	if lt == nil {
		return 0
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	success := lt.success[toolName]
	failure := lt.failure[toolName]
	total := success + failure
	if total == 0 {
		return 0
	}
	return float64(success) / float64(total)
}
