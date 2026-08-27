package agent

// Agent Performance Baseline & Regression Detection
//
// Trend: Agent performance observability is a hot area in 2025-2026. Devin's
// released /cost tracking. But NONE detect real-time performance regressions
// across sessions -- they only show current-run stats.
//
// Gap: ggcode's RunStats tracks rich per-run metrics (iterations, tool calls,
// errors, duration, compactions, context peak). But this data is NEVER compared
// against historical baselines. When the agent starts taking 2x more iterations
// for similar tasks -- due to prompt changes, tool regressions, model updates,
// or context bloat -- nobody notices until the user feels slowness.
//
// What this does:
//   1. PERSISTS: After each meaningful run, saves a compact summary to
//      .ggcode/perf-baseline.json (rolling window of last 50 runs).
//   2. DETECTS: At the start of the NEXT run, compares recent performance
//      against the historical baseline (median of the rolling window).
//   3. WARNS: If current performance has regressed on key metrics (iterations,
//      error rate, duration), injects a concise advisory so the agent can
//      adjust strategy (e.g., be more direct, avoid re-reading files).
//
// Competitor mapping:
//   - Claude Code: shows /cost per session, no cross-session comparison
//   - Cursor: no performance tracking
//   - Devin: SICA tracks efficiency internally, no user-visible regression alert
//   - Aider: no tracking
//
// Design:
//   - Zero LLM cost (deterministic statistical comparison)
//   - Fires at most once per session
//   - Only fires on REGRESSION (improvements are silent)
//   - Uses median (not mean) for robustness against outliers
//   - Requires at least 5 historical runs before comparing
//   - Persists to .ggcode/ directory (same as playbook, knight-memory, etc.)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	// perfBaselineMaxRuns is the rolling window size for historical data.
	perfBaselineMaxRuns = 50

	// perfBaselineMinRuns is the minimum number of historical runs needed
	// before regression detection activates. Too few samples → noisy baseline.
	perfBaselineMinRuns = 5

	// perfBaselineMaxWarns caps how many times the regression warning fires
	// per session. Once per session is enough — the agent should adapt.
	perfBaselineMaxWarns = 1

	// perfRegressionFactor is the multiplier over baseline median that
	// triggers a regression warning. 1.5x means the agent is 50% worse.
	perfRegressionFactor = 1.5

	// perfErrorRateFactor: if error rate (errors/tool_calls) exceeds baseline
	// by this factor, trigger a warning.
	perfErrorRateFactor = 2.0

	// perfBaselineFile is the filename for persisted baseline data.
	perfBaselineFile = "perf-baseline.json"
)

// perfBaselineEntry is a compact summary of a single run for trend analysis.
type perfBaselineEntry struct {
	RunID       string `json:"run_id"`
	Iterations  int    `json:"iter"`
	ToolCalls   int    `json:"tc"`
	Errors      int    `json:"err"`
	FilesEdited int    `json:"fe"`
	DurationSec int    `json:"dur"`
	Compactions int    `json:"cmp"`
	ContextPeak int    `json:"ctx"`
	Success     bool   `json:"ok"`
	Timestamp   int64  `json:"ts"`
}

// perfBaselineData is the on-disk JSON structure.
type perfBaselineData struct {
	Runs []perfBaselineEntry `json:"runs"`
}

// perfBaselineState tracks whether the regression warning has fired this session.
type perfBaselineState struct {
	warnCount int
	// #1180: session-level once-only flag. reset() runs on every user turn
	// and used to clear warnCount alone, so the same regression warning was
	// re-injected as a full user message on every subsequent turn - breaking
	// the "at most once per session" contract. This flag survives reset().
	warnedThisSession bool
	historical        []perfBaselineEntry // loaded once per run start
	baselineMid       perfBaselineEntry   // computed median of historical runs
	hasBaseline       bool
}

func newPerfBaselineState() *perfBaselineState {
	return &perfBaselineState{}
}

// reset() re-arms per-user-turn bookkeeping. #1180: it must NOT re-enable
// the regression warning (warnedThisSession survives), so the once-per-session
// contract holds across turns. It DOES drop the frozen in-memory snapshot
// (historical/baselineMid/hasBaseline) so the next run reloads fresh data from
// disk instead of replaying a stale conclusion; #1167's atomic write keeps
// that reload safe against concurrent writers.
func (p *perfBaselineState) reset() {
	p.warnCount = 0
	p.hasBaseline = false
	p.historical = nil
	p.baselineMid = perfBaselineEntry{}
}

// perfBaselinePath returns the full path to the baseline file.
func perfBaselinePath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", perfBaselineFile)
}

// loadPerfBaseline reads historical run data from disk. Returns empty data
// if the file doesn't exist or can't be parsed.
func loadPerfBaseline(workingDir string) []perfBaselineEntry {
	path := perfBaselinePath(workingDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var d perfBaselineData
	if err := json.Unmarshal(data, &d); err != nil {
		debug.Log("perf-baseline", "failed to parse %s: %v", path, err)
		return nil
	}
	return d.Runs
}

// savePerfBaseline writes the historical data to disk, atomically.
func savePerfBaseline(workingDir string, runs []perfBaselineEntry) {
	// Trim to rolling window.
	if len(runs) > perfBaselineMaxRuns {
		runs = runs[len(runs)-perfBaselineMaxRuns:]
	}
	path := perfBaselinePath(workingDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("perf-baseline", "failed to create dir: %v", err)
		return
	}
	d := perfBaselineData{Runs: runs}
	data, err := json.Marshal(d)
	if err != nil {
		debug.Log("perf-baseline", "failed to marshal: %v", err)
		return
	}
	// #1167: write via temp file + rename so a concurrent session never
	// observes a truncated or half-written baseline (which would silently
	// disable regression detection) and a failed write never leaves a
	// partial file behind. Same pattern as ratchet.go save().
	if err := util.AtomicWriteFile(path, data, 0644); err != nil {
		debug.Log("perf-baseline", "failed to write: %v", err)
	}
}

// computeMedianBaseline calculates the median of key metrics across historical runs.
// Only successful runs are included in the baseline to avoid skewing by failures.
func computeMedianBaseline(runs []perfBaselineEntry) perfBaselineEntry {
	// Filter to successful runs for a cleaner baseline.
	var valid []perfBaselineEntry
	for _, r := range runs {
		if r.Success {
			valid = append(valid, r)
		}
	}
	if len(valid) < perfBaselineMinRuns {
		return perfBaselineEntry{}
	}

	return perfBaselineEntry{
		Iterations:  medianInt(collectIterations(valid)),
		ToolCalls:   medianInt(collectToolCalls(valid)),
		Errors:      medianInt(collectErrors(valid)),
		FilesEdited: medianInt(collectFilesEdited(valid)),
		DurationSec: medianInt(collectDurations(valid)),
		Compactions: medianInt(collectCompactions(valid)),
		ContextPeak: medianInt(collectContextPeak(valid)),
	}
}

// collectX helpers extract a single field into a slice for median computation.
func collectIterations(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.Iterations
	}
	return out
}
func collectToolCalls(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.ToolCalls
	}
	return out
}
func collectErrors(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.Errors
	}
	return out
}
func collectFilesEdited(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.FilesEdited
	}
	return out
}
func collectDurations(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.DurationSec
	}
	return out
}
func collectCompactions(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.Compactions
	}
	return out
}
func collectContextPeak(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.ContextPeak
	}
	return out
}

// medianInt returns the median value of a slice of ints.
func medianInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int, len(vals))
	copy(sorted, vals)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// recordPerfBaseline saves a run summary to the historical data file.
// Called from maybeReflect (async, non-blocking).
func recordPerfBaseline(workingDir string, stats *RunStats) {
	if stats == nil {
		return
	}
	// Skip trivial runs (no meaningful work done).
	totalTC := stats.totalToolCalls()
	if totalTC < 3 && len(stats.FilesEdited) == 0 && len(stats.CommandsRun) == 0 {
		return
	}

	entry := perfBaselineEntry{
		RunID:       stats.RunID(),
		Iterations:  stats.Iterations,
		ToolCalls:   totalTC,
		Errors:      len(stats.Errors),
		FilesEdited: len(stats.FilesEdited),
		DurationSec: int(stats.Duration.Round(time.Second).Seconds()),
		Compactions: stats.CompactionCount,
		ContextPeak: stats.ContextPeakTokens,
		Success:     stats.Success,
		Timestamp:   time.Now().Unix(),
	}

	runs := loadPerfBaseline(workingDir)
	runs = append(runs, entry)
	// Trim to rolling window.
	if len(runs) > perfBaselineMaxRuns {
		runs = runs[len(runs)-perfBaselineMaxRuns:]
	}
	savePerfBaseline(workingDir, runs)
	debug.Log("perf-baseline", "recorded run %s: iter=%d tc=%d err=%d dur=%ds",
		entry.RunID, entry.Iterations, entry.ToolCalls, entry.Errors, entry.DurationSec)
}

// maybeInjectPerfRegression checks whether the agent's recent performance has
// regressed vs. the historical baseline. Called at run start (before iteration 1).
// Injects a concise advisory into the context if regression is detected.
func (a *Agent) maybeInjectPerfRegression() {
	if a.perfBaseline == nil {
		return
	}
	if a.perfBaseline.warnCount >= perfBaselineMaxWarns || a.perfBaseline.warnedThisSession {
		return
	}

	// Load historical data if not yet loaded this session.
	if !a.perfBaseline.hasBaseline {
		runs := loadPerfBaseline(a.WorkingDir())
		if len(runs) < perfBaselineMinRuns {
			return
		}
		a.perfBaseline.historical = runs
		a.perfBaseline.baselineMid = computeMedianBaseline(runs)
		a.perfBaseline.hasBaseline = true
	}

	mid := a.perfBaseline.baselineMid
	if mid.Iterations == 0 && mid.ToolCalls == 0 {
		return // baseline all zeros (shouldn't happen, but defensive)
	}

	// Check the last few completed runs for sustained regression.
	// A single bad run could be an outlier; sustained regression is real.
	// #1148: only successful runs vote - computeMedianBaseline excludes
	// failed runs (Success=false from iteration exhaustion, stream errors,
	// user cancels) because they skew high on iterations/duration/errors;
	// comparing failed-run metrics against a successful-run median is an
	// invalid cross-population contrast that systematically crossed every
	// regression threshold after any two consecutive failures.
	recent := a.perfBaseline.historical
	var recent3 []perfBaselineEntry
	for i := len(recent) - 1; i >= 0 && len(recent3) < 3; i-- {
		if recent[i].Success {
			recent3 = append(recent3, recent[i])
		}
	}
	if len(recent3) < 3 {
		return // not enough recent data
	}

	// Count how many of the last 3 runs exceeded regression thresholds,
	// bucketed per metric. Consensus requires at least 2 of 3 recent runs to
	// regress on the SAME metric -- cross-metric votes (run1 hits iterations,
	// run2 hits duration) must not pass the gate (#1143).
	metricCounts := make(map[string]int)
	for _, r := range recent3 {
		if _, metric := checkSingleRunRegression(r, mid); metric != "" {
			metricCounts[metric]++
		}
	}

	worstMetric := pickConsensusPerfMetric(metricCounts)
	if worstMetric == "" {
		debug.Log("perf-baseline", "regression candidates did not reach %d-run same-metric consensus", perfRegressionConsensusRuns)
		return
	}

	// Build the warning from a run that actually regressed on worstMetric --
	// the most severe one -- instead of blindly using the latest run (#1143).
	hitRun := selectWorstPerfHit(recent3, mid, worstMetric)

	a.perfBaseline.warnCount++
	a.perfBaseline.warnedThisSession = true // #1180: once per session, across resets
	msg := formatPerfRegressionWarning(worstMetric, mid, hitRun)
	debug.Log("perf-baseline", "regression detected: %d/3 recent runs regressed on %s", metricCounts[worstMetric], worstMetric)

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: msg,
		}},
	})
}

// checkSingleRunRegression checks if a single run regressed against baseline.
// Returns (true, metricName) if any key metric regressed.
func checkSingleRunRegression(run, baseline perfBaselineEntry) (bool, string) {
	// Iterations regression: 1.5x baseline
	if baseline.Iterations > 0 && run.Iterations > int(float64(baseline.Iterations)*perfRegressionFactor) {
		return true, "iterations"
	}
	// Duration regression: 1.5x baseline (skip if baseline is very short)
	if baseline.DurationSec > 10 && run.DurationSec > int(float64(baseline.DurationSec)*perfRegressionFactor) {
		return true, "duration"
	}
	// Error rate regression: 2x baseline error count.
	// #1143: removed the always-true "baseline.Errors >= 0" guard.
	if baseline.ToolCalls > 0 && run.Errors > 0 && run.ToolCalls > 0 {
		baseRate := float64(baseline.Errors) / float64(baseline.ToolCalls)
		runRate := float64(run.Errors) / float64(run.ToolCalls)
		if baseRate == 0 && runRate > 0.05 {
			// Baseline had 0 errors, current run has >5% error rate
			return true, "error_rate"
		}
		if baseRate > 0 && runRate > baseRate*perfErrorRateFactor {
			return true, "error_rate"
		}
	}
	// Context peak regression: context is growing 1.5x baseline
	if baseline.ContextPeak > 1000 && run.ContextPeak > int(float64(baseline.ContextPeak)*perfRegressionFactor) {
		return true, "context_usage"
	}
	// Compaction regression: significantly more compactions than baseline
	if baseline.Compactions == 0 && run.Compactions >= 3 {
		return true, "compaction"
	}
	return false, ""
}

// perfRegressionConsensusRuns is how many of the recent runs must regress on
// the SAME metric before a warning fires (#1143).
const perfRegressionConsensusRuns = 2

// perfMetricOrder lists regression metrics in the evaluation priority used by
// checkSingleRunRegression, keeping worst-metric selection deterministic
// when multiple metrics reach consensus (#1143).
var perfMetricOrder = []string{"iterations", "duration", "error_rate", "context_usage", "compaction"}

// pickConsensusPerfMetric returns a metric whose hit count reaches
// perfRegressionConsensusRuns across recent runs, preferring metrics earlier
// in perfMetricOrder when several qualify. Empty result means no metric
// reached same-metric consensus, so no warning should fire (#1143).
func pickConsensusPerfMetric(hitCounts map[string]int) string {
	for _, m := range perfMetricOrder {
		if hitCounts[m] >= perfRegressionConsensusRuns {
			return m
		}
	}
	return ""
}

// selectWorstPerfHit picks, among the recent runs that regressed on the given
// metric, the run with the most severe (largest) observed value for that
// metric. Falls back to the latest run defensively; normal callers gate on
// same-metric consensus first, so at least one hit always exists (#1143).
func selectWorstPerfHit(runs []perfBaselineEntry, baseline perfBaselineEntry, metric string) perfBaselineEntry {
	worst := runs[len(runs)-1]
	bestVal := -1
	for _, r := range runs {
		if _, m := checkSingleRunRegression(r, baseline); m != metric {
			continue
		}
		if v := perfMetricValue(r, metric); v > bestVal {
			bestVal = v
			worst = r
		}
	}
	return worst
}

// perfMetricValue extracts the observed value of a regression metric from a
// single run entry (#1143).
func perfMetricValue(entry perfBaselineEntry, metric string) int {
	switch metric {
	case "iterations":
		return entry.Iterations
	case "duration":
		return entry.DurationSec
	case "error_rate":
		return entry.Errors
	case "context_usage":
		return entry.ContextPeak
	case "compaction":
		return entry.Compactions
	default:
		return 0
	}
}

// formatPerfRegressionWarning builds a concise advisory message.
func formatPerfRegressionWarning(metric string, baseline perfBaselineEntry, latest perfBaselineEntry) string {
	switch metric {
	case "iterations":
		return formatPerfRegressionLine("iteration count",
			baseline.Iterations, latest.Iterations,
			"Be more direct: avoid redundant reads and searches. Plan before acting.")
	case "duration":
		return formatPerfRegressionLine("run duration (seconds)",
			baseline.DurationSec, latest.DurationSec,
			"Longer runs may indicate unnecessary rework. Verify changes incrementally.")
	case "error_rate":
		return formatPerfRegressionLine("error rate",
			baseline.Errors, latest.Errors,
			"High error rates suggest misjudging tool arguments. Double-check parameters before calling tools.")
	case "context_usage":
		return formatPerfRegressionLine("peak context tokens",
			baseline.ContextPeak, latest.ContextPeak,
			"Context bloat reduces quality. Use targeted reads (offset/limit) instead of full-file reads.")
	case "compaction":
		return formatPerfRegressionLine("compaction events",
			baseline.Compactions, latest.Compactions,
			"Frequent compaction means context is too large. Prefer narrow, targeted searches over broad exploration.")
	default:
		return ""
	}
}

func formatPerfRegressionLine(metricName string, baseline, current int, advice string) string {
	factor := 0.0
	if baseline > 0 {
		factor = float64(current) / float64(baseline)
	}
	out := "[Performance regression] " + metricName + " has regressed: "
	out += "baseline=" + intToStr(baseline) + ", recent=" + intToStr(current)
	if factor > 0 {
		out += " (" + trimZeros(floatToString(factor)) + "x baseline)"
	}
	out += ". " + advice
	return out
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func floatToString(f float64) string {
	// Simple formatting: show 1 decimal place
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	return intToStr(whole) + "." + intToStr(frac)
}

func trimZeros(s string) string {
	// Remove trailing ".0" for cleaner output.
	if len(s) >= 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}
