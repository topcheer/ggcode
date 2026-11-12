package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	perfBaselineMaxRuns = 50

	perfBaselineMinRuns = 5

	perfBaselineMaxWarns = 1

	perfRegressionFactor = 1.5

	perfErrorRateFactor = 2.0

	perfBaselineFile = "perf-baseline.json"

	perfTopToolsCount = 3
)

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

	Tokens int `json:"tok,omitempty"`

	TopTools []string `json:"top_tools,omitempty"`
}

type perfBaselineData struct {
	Runs []perfBaselineEntry `json:"runs"`
}

type perfBaselineState struct {
	warnCount         int
	warnedThisSession bool
	historical        []perfBaselineEntry
	baselineMid       perfBaselineEntry
	hasBaseline       bool
}

func newPerfBaselineState() *perfBaselineState {
	return &perfBaselineState{}
}

func (p *perfBaselineState) reset() {
	p.warnCount = 0
	p.hasBaseline = false
	p.historical = nil
	p.baselineMid = perfBaselineEntry{}
}

func perfBaselinePath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", perfBaselineFile)
}

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

func savePerfBaseline(workingDir string, runs []perfBaselineEntry) {

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

	if err := util.AtomicWriteFile(path, data, 0644); err != nil {
		debug.Log("perf-baseline", "failed to write: %v", err)
	}
}

func computeMedianBaseline(runs []perfBaselineEntry) perfBaselineEntry {
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
		Tokens:      medianInt(collectTokens(valid)),
		TopTools:    modalTopTools(valid),
	}
}

func modalTopTools(runs []perfBaselineEntry) []string {
	freq := make(map[string]int)
	withTools := 0
	for _, r := range runs {
		names := perfTopToolNames(r)
		if len(names) == 0 {
			continue
		}
		withTools++
		for _, n := range names {
			freq[n]++
		}
	}
	if withTools < 3 {
		return nil
	}
	type nameFreq struct {
		name string
		n    int
	}
	var cands []nameFreq
	half := (withTools + 1) / 2
	for n, c := range freq {
		if c >= half {
			cands = append(cands, nameFreq{n, c})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].name < cands[j].name
	})
	if len(cands) > perfTopToolsCount {
		cands = cands[:perfTopToolsCount]
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, fmt.Sprintf("%s:%d", c.name, c.n))
	}
	return out
}

func perfTopToolNames(e perfBaselineEntry) []string {
	if len(e.TopTools) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.TopTools))
	for _, s := range e.TopTools {
		if i := strings.LastIndex(s, ":"); i > 0 {
			names = append(names, s[:i])
		}
	}
	return names
}

func jaccardToolSets(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 1
	}
	setA := make(map[string]bool, len(a))
	for _, n := range a {
		setA[n] = true
	}
	inter := 0
	for _, n := range b {
		if setA[n] {
			inter++
		}
	}
	union := len(setA) + len(b) - inter
	return float64(inter) / float64(union)
}

const perfToolMixDriftFloor = 0.5

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

func collectTokens(runs []perfBaselineEntry) []int {
	out := make([]int, len(runs))
	for i, r := range runs {
		out[i] = r.Tokens
	}
	return out
}

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

func recordPerfBaseline(workingDir string, stats *RunStats) {
	if stats == nil {
		return
	}

	totalTC := stats.totalToolCalls()
	if totalTC < 3 && len(stats.FilesEdited) == 0 && len(stats.CommandsRun) == 0 {
		return
	}

	entry := perfBaselineEntry{
		RunID:      stats.RunID(),
		Iterations: stats.Iterations,
		ToolCalls:  totalTC,

		Errors:      stats.ErrorCount,
		FilesEdited: len(stats.FilesEdited),
		DurationSec: int(stats.ActiveDuration().Round(time.Second).Seconds()),
		Compactions: stats.CompactionCount,
		ContextPeak: stats.ContextPeakTokens,
		Success:     stats.Success,
		Timestamp:   time.Now().Unix(),
		Tokens:      stats.TotalTokens,
		TopTools:    topToolMix(stats.ToolCalls, perfTopToolsCount),
	}

	runs := loadPerfBaseline(workingDir)
	runs = append(runs, entry)

	if len(runs) > perfBaselineMaxRuns {
		runs = runs[len(runs)-perfBaselineMaxRuns:]
	}
	savePerfBaseline(workingDir, runs)
	debug.Log("perf-baseline", "recorded run %s: iter=%d tc=%d err=%d dur=%ds",
		entry.RunID, entry.Iterations, entry.ToolCalls, entry.Errors, entry.DurationSec)
}

func (a *Agent) maybeInjectPerfRegression() {
	if a.perfBaseline == nil {
		return
	}
	if a.perfBaseline.warnCount >= perfBaselineMaxWarns || a.perfBaseline.warnedThisSession {
		return
	}

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
		return
	}

	recent := a.perfBaseline.historical
	var recent3 []perfBaselineEntry
	for i := len(recent) - 1; i >= 0 && len(recent3) < 3; i-- {
		if recent[i].Success {
			recent3 = append(recent3, recent[i])
		}
	}
	if len(recent3) < 3 {
		return
	}

	metricCounts := make(map[string]int)
	for _, r := range recent3 {
		for _, m := range collectRunRegressionMetrics(r, mid) {
			metricCounts[m]++
		}
	}

	worstMetric := pickConsensusPerfMetric(metricCounts)
	if worstMetric == "" {
		debug.Log("perf-baseline", "regression candidates did not reach %d-run same-metric consensus", perfRegressionConsensusRuns)
		return
	}

	hitRun := selectWorstPerfHit(recent3, mid, worstMetric)

	a.perfBaseline.warnCount++
	a.perfBaseline.warnedThisSession = true
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

func checkSingleRunRegression(run, baseline perfBaselineEntry) (bool, string) {
	if hits := collectRunRegressionMetrics(run, baseline); len(hits) > 0 {
		return true, hits[0]
	}
	return false, ""
}

func collectRunRegressionMetrics(run, baseline perfBaselineEntry) []string {
	var hits []string

	if baseline.Iterations > 0 && run.Iterations > int(float64(baseline.Iterations)*perfRegressionFactor) {
		hits = append(hits, "iterations")
	}

	if baseline.DurationSec > 10 && run.DurationSec > int(float64(baseline.DurationSec)*perfRegressionFactor) {
		hits = append(hits, "duration")
	}

	if baseline.ToolCalls > 0 && run.Errors > 0 && run.ToolCalls > 0 {
		baseRate := float64(baseline.Errors) / float64(baseline.ToolCalls)
		runRate := float64(run.Errors) / float64(run.ToolCalls)
		if baseRate == 0 && runRate > 0.05 {

			hits = append(hits, "error_rate")
		} else if baseRate > 0 && runRate > baseRate*perfErrorRateFactor {
			hits = append(hits, "error_rate")
		}
	}

	if baseline.Tokens >= 10000 && run.Tokens > baseline.Tokens*2 {
		hits = append(hits, "tokens")
	}

	if baseline.ContextPeak > 1000 && run.ContextPeak > int(float64(baseline.ContextPeak)*perfRegressionFactor) {
		hits = append(hits, "context_usage")
	}

	if baseline.Compactions == 0 && run.Compactions >= 3 {
		hits = append(hits, "compaction")
	}

	if jaccardToolSets(perfTopToolNames(run), perfTopToolNames(baseline)) < perfToolMixDriftFloor {
		hits = append(hits, "tool_mix")
	}
	return hits
}

const perfRegressionConsensusRuns = 2

var perfMetricOrder = []string{"iterations", "duration", "error_rate", "tokens", "context_usage", "compaction", "tool_mix"}

func pickConsensusPerfMetric(hitCounts map[string]int) string {
	for _, m := range perfMetricOrder {
		if hitCounts[m] >= perfRegressionConsensusRuns {
			return m
		}
	}
	return ""
}

func selectWorstPerfHit(runs []perfBaselineEntry, baseline perfBaselineEntry, metric string) perfBaselineEntry {
	worst := runs[len(runs)-1]
	bestVal := -1
	for _, r := range runs {

		hit := false
		for _, m := range collectRunRegressionMetrics(r, baseline) {
			if m == metric {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		var v int
		if metric == "tool_mix" {

			v = 100 - int(jaccardToolSets(perfTopToolNames(r), perfTopToolNames(baseline))*100)
		} else {
			v = perfMetricValue(r, metric)
		}
		if v > bestVal {
			bestVal = v
			worst = r
		}
	}
	return worst
}

func perfMetricValue(entry perfBaselineEntry, metric string) int {
	switch metric {
	case "iterations":
		return entry.Iterations
	case "duration":
		return entry.DurationSec
	case "error_rate":
		return entry.Errors
	case "tokens":
		return entry.Tokens
	case "context_usage":
		return entry.ContextPeak
	case "compaction":
		return entry.Compactions
	default:
		return 0
	}
}

func formatPerfRegressionWarning(metric string, baseline perfBaselineEntry, latest perfBaselineEntry) string {
	switch metric {
	case "iterations":
		return formatPerfRegressionLine("iteration count",
			baseline.Iterations, latest.Iterations,
			"Be more direct: avoid redundant reads and searches. Plan before acting.") +
			formatRunShapeDiagnostics(latest, 0)
	case "duration":
		return formatPerfRegressionLine("run duration (seconds)",
			baseline.DurationSec, latest.DurationSec,
			"Longer runs may indicate unnecessary rework. Verify changes incrementally.") +
			formatRunShapeDiagnostics(latest, latest.DurationSec)
	case "error_rate":
		return formatPerfRegressionLine("error rate",
			baseline.Errors, latest.Errors,
			"High error rates suggest misjudging tool arguments. Double-check parameters before calling tools.")
	case "tokens":
		return formatPerfRegressionLine("total LLM tokens",
			baseline.Tokens, latest.Tokens,
			"Token spend jumped vs baseline: suspect prompt/context bloat, a fallback chain landing on a pricier model, or lost prompt caching. Prefer targeted reads and reuse established context.")
	case "context_usage":
		return formatPerfRegressionLine("peak context tokens",
			baseline.ContextPeak, latest.ContextPeak,
			"Context bloat reduces quality. Use targeted reads (offset/limit) instead of full-file reads.")
	case "compaction":
		return formatPerfRegressionLine("compaction events",
			baseline.Compactions, latest.Compactions,
			"Frequent compaction means context is too large. Prefer narrow, targeted searches over broad exploration.")
	case "tool_mix":

		return fmt.Sprintf("Performance regression notice: dominant tool mix drifted from the project baseline. Baseline modal tools: [%s]; this run: [%s]. The workflow this project historically used has shifted - if this is not a deliberate change of approach, return to the established tool pattern (targeted reads/searches before broad commands).",
			strings.Join(perfTopToolNames(baseline), ", "),
			strings.Join(perfTopToolNames(latest), ", "))
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

func topToolMix(m map[string]int, n int) []string {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > n {
		names = names[:n]
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = name + ":" + intToStr(m[name])
	}
	return out
}

func formatRunShapeDiagnostics(hit perfBaselineEntry, durSec int) string {
	out := " Run shape of the regressed run: iterations=" + intToStr(hit.Iterations)
	out += ", tool_calls=" + intToStr(hit.ToolCalls)
	if durSec > 0 && hit.Iterations > 0 {
		out += ", sec/iter≈" + trimZeros(floatToString(float64(durSec)/float64(hit.Iterations)))
	}
	if len(hit.TopTools) > 0 {
		out += "; top tools: " + strings.Join(hit.TopTools, ", ")
		out += ". Target the dominant tool pattern first (batch shell commands, narrow reads) and stop once the change verifies."
	}
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

	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	return intToStr(whole) + "." + intToStr(frac)
}

func trimZeros(s string) string {

	if len(s) >= 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}
