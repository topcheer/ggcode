// Package runeval implements offline, deterministic trajectory evaluation for
// a completed (or in-progress) agent run.
//
// Frontier agent-evaluation practice (Langfuse "AI agent evaluation", 2025-26)
// scores agents on the trajectory — the path of steps — in addition to the
// final outcome, because tool-use failures and redundant calls are often
// invisible in the final answer: the agent recovers, but the retry burned
// tokens the user pays for. Trajectory-level signals include step count,
// unnecessary (duplicate) tool calls, loops/retries, and tool error rate.
// Deterministic code checks cover exactly these signals at zero judge cost.
//
// ggcode already had runtime metacognition (trajectory_health detectors that
// steer the live loop) and post-run qualitative reflection (run-insights
// memory). What was missing is the quantitative offline scorecard: given the
// session's full message log, quantify duplicate calls, failed calls, and the
// context tax they impose, then emit a transparent efficiency score plus
// actionable findings. runeval is that scorecard — pure analysis, no LLM
// calls, no runtime steering (it is not a detector; the user invokes it via
// the /runreport slash command).
//
// 2026 per-dimension scoring practice (FutureAGI "definitive guide to AI
// agent evaluation", τ-bench-style trajectory grading) scores multiple axes
// independently, because a single aggregate hides which dimension regressed.
// runeval therefore reports two axes: EFFICIENCY (the context tax of
// redundant work) and RELIABILITY (error recovery, rework churn, and
// late-run degradation), each with transparent penalties.
package runeval

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// UsageSample is a provider-agnostic per-LLM-call usage record. The TUI maps
// session.UsageEntry entries onto this so runeval does not depend on the
// session package (and stays trivially unit-testable).
type UsageSample struct {
	Source string // "agent", "compaction", "strategist", "verify", "subagent", ...
	Usage  provider.TokenUsage
}

// DuplicateGroup describes a tool+input key that was invoked more than once
// within the evaluated window.
type DuplicateGroup struct {
	Tool      string // tool name
	Input     string // canonical input JSON, display-truncated
	Count     int    // total identical invocations
	Repeats   int    // Count-1 redundant invocations
	Identical bool   // every observed result was byte-identical
	ReadOnly  bool   // tool classified as read-only
}

// FileChurn counts write-tool invocations against a single file. Rework —
// editing the same file repeatedly — is the offline signature of unstable
// assumptions and the strongest predictor of late-run failure spirals.
type FileChurn struct {
	File  string
	Edits int
}

// Report is the trajectory evaluation output.
type Report struct {
	// TurnCount counts assistant messages carrying at least one tool call —
	// the agent-loop steps the trajectory consumed.
	TurnCount int

	// ToolCalls / ToolErrors count tool_use blocks and IsError results.
	ToolCalls  int
	ToolErrors int

	// DistinctTools counts unique tool names invoked.
	DistinctTools int

	// DuplicateGroups lists keys invoked more than once, worst first.
	DuplicateGroups []DuplicateGroup

	// WastedRepeatCalls counts redundant invocations whose result added no
	// new information: failed (IsError) results and byte-identical results
	// on read-only tools.
	WastedRepeatCalls int

	// WastedResultBytes sums the output bytes of those no-information
	// results — text that gets re-ingested into every subsequent request's
	// context without changing what the model knows.
	WastedResultBytes int

	// WastedTokenEstimate approximates the context tax at ~4 bytes/token.
	WastedTokenEstimate int

	// OverheadTokens / TotalTokens split usage by Source. Overhead is any
	// non-"agent" source (compaction, strategist, verify, ...).
	OverheadTokens int
	TotalTokens    int

	// EfficiencyScore is 100 minus transparent penalties (see Evaluate).
	// 0-100; 100 = clean trajectory.
	EfficiencyScore int

	// Reliability axis — the error-recovery dimension of the scorecard.
	//
	// RetrySameInput counts invocations that re-sent an input that had
	// already failed identically before (an uncorrected retry — the
	// anti-pattern behind most error spirals). The first failure of any
	// call is information; only re-sends count. WorstRetry names the worst
	// offending call for display, empty when there is none.
	RetrySameInput int
	WorstRetry     string

	// ChurnedFiles lists files written by write-tools more than once,
	// worst first; ChurnExtraEdits sums edits beyond each file's first.
	ChurnedFiles    []FileChurn
	ChurnExtraEdits int

	// Degraded is true when tool errors concentrate in the later half of
	// the run (enough errors to clear the noise floor) — the trajectory
	// got worse over time instead of recovering.
	Degraded bool

	// ReliabilityScore is 100 minus transparent penalties (see
	// reliabilityScore). 0-100; 100 = clean recovery behavior.
	ReliabilityScore int

	// Findings are human-readable, worst-first improvement hints.
	Findings []string
}

// callRecord tracks one distinct (tool, canonical-input) key across its
// invocations and paired results.
type callRecord struct {
	tool      string
	canonical string
	count     int
	hashes    map[string]bool // truncated result contents observed
	readOnly  bool
}

func (c *callRecord) identical() bool { return len(c.hashes) == 1 }

// callKey identifies a call record.
type callKey struct {
	tool  string
	input string
}

const (
	// bytesPerToken is the classical ~4-chars-per-token approximation used
	// for the context-tax estimate. Deliberately coarse; the report states
	// it is an estimate.
	bytesPerToken = 4

	// displayInputCap truncates the canonical input shown in findings.
	displayInputCap = 96

	// resultHashCap bounds the per-result identity hash to keep comparison
	// cost linear and small.
	resultHashCap = 256

	// minToolsForOverheadPenalty guards the overhead-share penalty against
	// small-sample noise (a 2-call session has no meaningful overhead).
	minToolsForOverheadPenalty = 5

	// Reliability penalty caps: no single axis component should zero out
	// the score by itself (mirrors the efficiency score's weight design).
	maxRetryPenalty = 36
	maxChurnPenalty = 32

	// minErrorsForDegradation keeps the half-split trend test away from
	// small-sample noise (one early + one late error is not a trend).
	minErrorsForDegradation = 3

	// degradationPenalty subtracted when the late-half error rate exceeds
	// the early-half rate.
	degradationPenalty = 12
)

// readOnlyTools is the conservative whitelist of tools whose repeated
// identical results carry zero new information. Unknown tools are treated as
// potentially stateful: their repeats are reported as duplicate groups but
// never auto-counted as wasted calls.
var readOnlyTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"search_files":    true,
	"grep":            true,
	"glob":            true,
	"list_directory":  true,
	"code_search":     true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_references":  true,
	"lsp_definition":  true,
	"lsp_diagnostics": true,
	"git_status":      true,
	"git_log":         true,
	"git_diff":        true,
	"git_blame":       true,
	"task_list":       true,
	"list_worktree":   true,
	"web_search":      true,
	"knowledge_list":  true,
	"knowledge_query": true,
	"memory_list":     true,
	"memory_query":    true,
	"endpoint_stats":  true,
	"screenshot":      true,
	"snapshot_ui":     true,
	"find_element":    true,
	"list_windows":    true,
	"display_info":    true,
}

// writeTools is the whitelist of single-file write tools whose input carries
// a file path, used for the churn (rework) axis. Multi-file batch tools are
// deliberately excluded: their per-file edit counts are not visible at the
// tool-call level and would understate churn unpredictably.
var writeTools = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"notebook_edit":   true,
}

// inputPath extracts the file path from a write-tool input JSON. Returns ""
// when the input carries no recognizable path field.
func inputPath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "notebook_path"} {
		if v, ok := m[k]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

// Evaluate walks the full message log and produces a Report. It is pure:
// same input, same output, no side effects, no LLM calls.
func Evaluate(msgs []provider.Message, usage []UsageSample) Report {
	var r Report

	byID := map[string]*callKey{}        // tool_use ID -> its record key
	records := map[callKey]*callRecord{} // distinct call keys
	var keyOrder []callKey               // first-appearance order

	// resultObs is one ordered tool_result observation; wasted bytes are
	// decided against record state learned during the same walk.
	type resultObs struct {
		key     callKey
		isError bool
		outLen  int
	}
	var results []resultObs

	for i := range msgs {
		for _, b := range msgs[i].Content {
			switch b.Type {
			case "tool_use":
				key := callKey{
					tool:  b.ToolName,
					input: canonicalJSON(b.Input),
				}
				byID[b.ToolID] = &key
				rec := records[key]
				if rec == nil {
					rec = &callRecord{
						tool:      key.tool,
						canonical: key.input,
						hashes:    map[string]bool{},
						readOnly:  readOnlyTools[key.tool],
					}
					records[key] = rec
					keyOrder = append(keyOrder, key)
				}
				rec.count++
				r.ToolCalls++
			case "tool_result":
				key := byID[b.ToolID]
				if key == nil {
					continue // unpaired result (e.g. truncated log) — skip
				}
				rec := records[*key]
				if rec == nil {
					continue
				}
				rec.hashes[truncateHash(b.Output)] = true
				if b.IsError {
					r.ToolErrors++
				}
				results = append(results, resultObs{
					key:     *key,
					isError: b.IsError,
					outLen:  len(b.Output),
				})
			}
		}
	}

	// Distinct tools.
	seenTools := map[string]bool{}
	for _, k := range keyOrder {
		seenTools[records[k].tool] = true
	}
	r.DistinctTools = len(seenTools)

	// Turn count: assistant messages that issued at least one tool call.
	for i := range msgs {
		if msgs[i].Role != "assistant" {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" {
				r.TurnCount++
				break
			}
		}
	}

	// Churn (rework) axis: count write-tool invocations per file.
	churn := map[string]int{}
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" && writeTools[b.ToolName] {
				if p := inputPath(b.Input); p != "" {
					churn[p]++
				}
			}
		}
	}
	for p, n := range churn {
		if n >= 2 {
			r.ChurnedFiles = append(r.ChurnedFiles, FileChurn{File: p, Edits: n})
			r.ChurnExtraEdits += n - 1
		}
	}
	sort.Slice(r.ChurnedFiles, func(a, b int) bool {
		if r.ChurnedFiles[a].Edits != r.ChurnedFiles[b].Edits {
			return r.ChurnedFiles[a].Edits > r.ChurnedFiles[b].Edits
		}
		return r.ChurnedFiles[a].File < r.ChurnedFiles[b].File
	})

	// A call key's LAST result is the informative one: the final state the
	// agent actually learned from. Every earlier result of the same key is a
	// superseded attempt - wasted when it failed (no usable information) or
	// when it is an identical read-only repeat (duplicate of what the last
	// call returns anyway).
	lastIdx := map[callKey]int{}
	for i, res := range results {
		lastIdx[res.key] = i
	}
	for i, res := range results {
		rec := records[res.key]
		if rec == nil || i == lastIdx[res.key] {
			continue
		}
		if res.isError || (rec.readOnly && rec.identical()) {
			r.WastedRepeatCalls++
			r.WastedResultBytes += res.outLen
		}
	}

	// Reliability axis: uncorrected retries (an input that already failed
	// identically before is sent again) and the early-vs-late error trend.
	errByKey := map[callKey]int{}
	half := len(results) / 2
	var firstErrs, secondErrs int
	for i, res := range results {
		if !res.isError {
			continue
		}
		errByKey[res.key]++
		if i < half {
			firstErrs++
		} else {
			secondErrs++
		}
	}
	// Deterministic worst-selection: iterate keys in sorted order and only
	// replace the worst on a strictly greater count.
	retryKeys := make([]callKey, 0, len(errByKey))
	for k, n := range errByKey {
		if n >= 2 {
			retryKeys = append(retryKeys, k)
		}
	}
	sort.Slice(retryKeys, func(a, b int) bool {
		if retryKeys[a].tool != retryKeys[b].tool {
			return retryKeys[a].tool < retryKeys[b].tool
		}
		return retryKeys[a].input < retryKeys[b].input
	})
	var worst callKey
	worstN := 1
	for _, k := range retryKeys {
		n := errByKey[k]
		r.RetrySameInput += n - 1
		if n > worstN {
			worst, worstN = k, n
		}
	}
	if worstN > 1 {
		r.WorstRetry = fmt.Sprintf("%s(%s) ×%d", worst.tool, truncateDisplay(worst.input), worstN)
	}
	if firstErrs+secondErrs >= minErrorsForDegradation && half > 0 {
		// late-half error rate strictly above early-half rate, compared by
		// cross-multiplication to stay in integer arithmetic
		if secondErrs*half > firstErrs*(len(results)-half) {
			r.Degraded = true
		}
	}

	for _, k := range keyOrder {
		rec := records[k]
		if rec.count < 2 {
			continue
		}
		r.DuplicateGroups = append(r.DuplicateGroups, DuplicateGroup{
			Tool:      rec.tool,
			Input:     truncateDisplay(rec.canonical),
			Count:     rec.count,
			Repeats:   rec.count - 1,
			Identical: rec.identical(),
			ReadOnly:  rec.readOnly,
		})
	}
	sort.Slice(r.DuplicateGroups, func(a, b int) bool {
		if r.DuplicateGroups[a].Repeats != r.DuplicateGroups[b].Repeats {
			return r.DuplicateGroups[a].Repeats > r.DuplicateGroups[b].Repeats
		}
		return r.DuplicateGroups[a].Tool < r.DuplicateGroups[b].Tool
	})

	r.WastedTokenEstimate = r.WastedResultBytes / bytesPerToken

	// Usage split.
	for _, s := range usage {
		t := s.Usage.Total()
		r.TotalTokens += t
		if s.Source != "" && s.Source != "agent" {
			r.OverheadTokens += t
		}
	}

	r.EfficiencyScore = score(r)
	r.ReliabilityScore = reliabilityScore(r)
	r.Findings = findings(r)
	return r
}

// score is the transparent composite: 100 minus
//   - 40 × tool error rate,
//   - 30 × wasted-repeat rate,
//   - 20 × overhead share (only when the trajectory is big enough for the
//     ratio to be meaningful),
//
// clamped to [0,100]. Weights reflect that a failed call both burns output
// tokens and forces a retry turn (most damaging), duplicate reads are pure
// context tax, and overhead machinery is by design.
func score(r Report) int {
	s := 100.0
	if r.ToolCalls > 0 {
		s -= 40 * float64(r.ToolErrors) / float64(r.ToolCalls)
		s -= 30 * float64(r.WastedRepeatCalls) / float64(r.ToolCalls)
	}
	if r.ToolCalls >= minToolsForOverheadPenalty && r.TotalTokens > 0 {
		s -= 20 * float64(r.OverheadTokens) / float64(r.TotalTokens)
	}
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return int(s + 0.5)
}

// reliabilityScore is the transparent reliability composite: 100 minus
//   - 12 per uncorrected retry (re-sending an input that already failed),
//     capped at 36,
//   - 8 per rework edit beyond a file's first write, capped at 32,
//   - 12 when the run degrades (late-half error rate above early-half).
//
// Weights mirror the efficiency score: uncorrected retries are the classic
// error-spiral signature (most damaging), rework churn signals unstable
// assumptions, and the degradation flag catches trajectories that got worse
// instead of recovering.
func reliabilityScore(r Report) int {
	s := 100.0
	retryPenalty := 12 * float64(r.RetrySameInput)
	if retryPenalty > maxRetryPenalty {
		retryPenalty = maxRetryPenalty
	}
	s -= retryPenalty
	churnPenalty := 8 * float64(r.ChurnExtraEdits)
	if churnPenalty > maxChurnPenalty {
		churnPenalty = maxChurnPenalty
	}
	s -= churnPenalty
	if r.Degraded {
		s -= degradationPenalty
	}
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return int(s + 0.5)
}

// findings renders worst-first, quantified improvement hints.
func findings(r Report) []string {
	var f []string

	shown := 0
	for _, g := range r.DuplicateGroups {
		if g.Repeats <= 0 || shown >= 3 {
			continue
		}
		noun := "calls"
		if g.Repeats == 1 {
			noun = "call"
		}
		if g.Identical && g.ReadOnly {
			f = append(f, fmt.Sprintf(
				"%d repeated %s %s(%s) returned identical results — the answer was already in context",
				g.Repeats, noun, g.Tool, g.Input))
		} else {
			f = append(f, fmt.Sprintf(
				"%d repeated %s %s(%s) — hit the same inputs %d×; check why earlier results were insufficient",
				g.Repeats, noun, g.Tool, g.Input, g.Count))
		}
		shown++
	}

	if r.ToolErrors > 0 {
		f = append(f, fmt.Sprintf(
			"%d of %d tool calls failed — each failure costs a retry turn plus the error text re-ingested into context",
			r.ToolErrors, r.ToolCalls))
	}

	if r.TotalTokens > 0 && r.ToolCalls >= minToolsForOverheadPenalty {
		share := 100 * float64(r.OverheadTokens) / float64(r.TotalTokens)
		if share >= 15 {
			f = append(f, fmt.Sprintf(
				"%.0f%% of tokens came from non-agent machinery (compaction/strategist/verify…) — weigh the cost-quality tradeoff of that overhead",
				share))
		}
	}

	if r.WastedTokenEstimate > 0 {
		f = append(f, fmt.Sprintf(
			"~%s tokens of context tax from failed/duplicate results (~%s bytes)",
			comma(r.WastedTokenEstimate), comma(r.WastedResultBytes)))
	}

	if r.RetrySameInput > 0 {
		f = append(f, fmt.Sprintf(
			"%d call(s) re-sent an input that had already failed (worst: %s) — fix the arguments or change approach instead of retrying identically",
			r.RetrySameInput, r.WorstRetry))
	}

	if len(r.ChurnedFiles) > 0 {
		w := r.ChurnedFiles[0]
		f = append(f, fmt.Sprintf(
			"%d file(s) written more than once (%d rework edits; worst: %s ×%d) — repeated rework of the same file signals unstable assumptions",
			len(r.ChurnedFiles), r.ChurnExtraEdits, w.File, w.Edits))
	}

	if r.Degraded {
		f = append(f, "tool errors concentrated in the later half of the run — the trajectory degraded instead of recovering")
	}

	return f
}

// Render formats the report as a compact scorecard for chat display.
func Render(r Report) string {
	var b strings.Builder
	// The grade reflects the weaker axis: a run cannot be "excellent" while
	// one dimension is failing (per-dimension scoring practice).
	weaker := r.EfficiencyScore
	if r.ReliabilityScore < weaker {
		weaker = r.ReliabilityScore
	}
	grade := "good"
	switch {
	case weaker >= 90:
		grade = "excellent"
	case weaker < 60:
		grade = "needs work"
	}
	fmt.Fprintf(&b, "Run report — trajectory scorecard (efficiency %d/100 · reliability %d/100, %s)\n",
		r.EfficiencyScore, r.ReliabilityScore, grade)
	fmt.Fprintf(&b, "  Steps: %d turns with tool calls · %d tool calls · %d distinct tools\n",
		r.TurnCount, r.ToolCalls, r.DistinctTools)
	fmt.Fprintf(&b, "  Failures: %d/%d tool calls errored · wasted repeats: %d\n",
		r.ToolErrors, r.ToolCalls, r.WastedRepeatCalls)
	fmt.Fprintf(&b, "  Reliability: %d uncorrected retry(s) · %d rework edit(s) across %d file(s)",
		r.RetrySameInput, r.ChurnExtraEdits, len(r.ChurnedFiles))
	if r.Degraded {
		b.WriteString(" · degraded trend")
	}
	b.WriteString("\n")
	if r.TotalTokens > 0 {
		share := 100 * float64(r.OverheadTokens) / float64(r.TotalTokens)
		fmt.Fprintf(&b, "  Tokens: %s total · %.0f%% overhead machinery · ~%s wasted context tax (est.)\n",
			comma(r.TotalTokens), share, comma(r.WastedTokenEstimate))
	}
	if len(r.DuplicateGroups) > 0 {
		fmt.Fprintf(&b, "  Duplicate groups: %d (worst: %s ×%d)\n",
			len(r.DuplicateGroups), r.DuplicateGroups[0].Tool, r.DuplicateGroups[0].Count)
	} else {
		b.WriteString("  Duplicate groups: none\n")
	}
	if len(r.Findings) > 0 {
		b.WriteString("Findings:\n")
		for i, line := range r.Findings {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// canonicalJSON normalizes raw input JSON so semantically identical inputs
// (key order/whitespace differences) hash to the same key. Non-JSON inputs
// degrade to the raw string.
func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	// encoding/json marshals map[string]any with sorted keys, giving a
	// stable canonical form without extra dependencies.
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// truncateHash bounds result identity comparison to a prefix.
func truncateHash(s string) string {
	if len(s) > resultHashCap {
		return s[:resultHashCap]
	}
	return s
}

// truncateDisplay bounds canonical input shown in findings.
func truncateDisplay(s string) string {
	if s == "" {
		return "∅"
	}
	if len(s) > displayInputCap {
		return s[:displayInputCap] + "…"
	}
	return s
}

func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}
