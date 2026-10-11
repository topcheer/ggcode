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

// outlierRunThreshold is how many CONSECUTIVE statistical outliers a tool
// must log before its samples are admitted into the rolling baseline as the
// new normal (#3689): one/two-off stalls stay excluded (detection stays
// sharp), a consistently slow environment shifts the baseline (adaptive).
const outlierRunThreshold = 3

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
	mu         sync.Mutex
	samples    map[string][]latencySample // tool name → recent durations
	lastWarn   map[string]time.Time       // tool name → last warning time (cooldown)
	outlierRun map[string]int             // tool name → consecutive statistical outliers (#3689)
	success    map[string]int             // tool name → successful calls count
	failure    map[string]int             // tool name → failed calls count
}

// NewLatencyTracker creates a ready-to-use LatencyTracker.
func NewLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		samples:    make(map[string][]latencySample),
		lastWarn:   make(map[string]time.Time),
		outlierRun: make(map[string]int),
		success:    make(map[string]int),
		failure:    make(map[string]int),
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
	isOutlier := false
	if latencyMonitoredTools[toolName] {
		warning, isOutlier = lt.checkOutlier(toolName, dur, samples)
	}

	// Record latency for ALL tools (used by adaptive timeout computation).
	// #3639: an outlier sample does NOT enter the rolling window. Appending
	// it unconditionally polluted the baseline (one 60s stall lifted the
	// mean to ~15s, and the NEXT genuine 20s outlier no longer cleared the
	// 5x bar - a detection blind spot). The warning for the current event
	// already fired above; the window keeps tracking the healthy baseline.
	// #3689: the exclusion is judged from the STATISTICAL signal (not the
	// warning - cooldown suppressed warnings let outliers back in), with
	// one carve-out: after outlierRunThreshold CONSECUTIVE outliers the
	// samples are admitted as the new normal (a consistently slow
	// environment - network mount - must shift the baseline upward, which
	// TestLatencyTrackerAdaptsBaseline pins; one/two-off stalls stay out).
	if !isOutlier || lt.outlierRun[toolName] >= outlierRunThreshold {
		samples = append(samples, latencySample{dur: dur, recorded: time.Now()})
		if len(samples) > maxLatencySamples {
			samples = samples[len(samples)-maxLatencySamples:]
		}
	}
	// Maintain the consecutive-outlier run (#3689): reset on a normal
	// sample, increment on a statistical outlier regardless of cooldown.
	if isOutlier {
		lt.outlierRun[toolName]++
	} else {
		lt.outlierRun[toolName] = 0
	}
	lt.samples[toolName] = samples

	return warning
}

// checkOutlier determines whether dur is an outlier relative to the existing
// sample baseline, and returns the warning to surface (empty when cooldown
// suppresses it). The second return is the STATISTICAL outlier signal - true
// whenever dur clears the threshold, regardless of cooldown (#3689: the
// window-exclusion invariant must hold on the cooldown path too; judging it
// from warning != "" let cooldown-suppressed outliers pollute the baseline).
// Caller must hold lt.mu.
func (lt *LatencyTracker) checkOutlier(toolName string, dur time.Duration, samples []latencySample) (string, bool) {
	// Not enough baseline data.
	if len(samples) < latencyMinSamples {
		return "", false
	}

	// Below absolute floor — never warn for fast operations.
	if dur < latencyAbsoluteFloor {
		return "", false
	}

	// Compute mean of existing samples.
	var total time.Duration
	for _, s := range samples {
		total += s.dur
	}
	mean := total / time.Duration(len(samples))
	if mean <= 0 {
		return "", false
	}

	// Only warn when dramatically slower than baseline.
	if float64(dur) < float64(mean)*latencyOutlierMultiplier {
		return "", false
	}

	// Cooldown: don't warn about the same tool repeatedly - but the sample
	// is STILL a statistical outlier (window exclusion continues to apply).
	if last, ok := lt.lastWarn[toolName]; ok {
		if time.Since(last) < latencyWarnCooldown {
			return "", true
		}
	}
	lt.lastWarn[toolName] = time.Now()

	return formatLatencyWarning(toolName, dur, mean), true
}

// formatLatencyWarning builds a concise, actionable hint for the agent.
func formatLatencyWarning(toolName string, dur, mean time.Duration) string {
	ratio := float64(dur) / float64(mean)
	if ratio < 1 {
		ratio = 1
	}

	var hint string
	switch {
	// #3639: edit BEFORE the read/file branch - edit_file, multi_edit_file
	// and multi_file_edit all contain "file" and used to fall into the
	// read hint ("use offset/limit"), leaving the edit branch unreachable
	// dead code for every tool in the monitored set.
	case strings.Contains(toolName, "edit"):
		hint = "this should normally be fast — check if the file is unusually large."
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "file"):
		hint = "consider using offset/limit to read only the relevant section."
	case strings.Contains(toolName, "search") || strings.Contains(toolName, "grep") || toolName == "glob":
		hint = "consider narrowing the search pattern or directory scope."
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
