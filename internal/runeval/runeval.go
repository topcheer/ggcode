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
// Multi-axis extension: the scorecard also evaluates grounding — a
// deterministic, judge-free check inspired by GroundEval (arXiv:2606.22737,
// 2026): file paths cited in the final answer are cross-checked against
// the run's tool trace (tools that opened/edited them, or tool results
// that surfaced them). Cited paths with no trace presence are reported
// as ungrounded citations — plausible answers resting on invalid
// evidence paths, which final-answer and judge-based evaluation miss by
// construction. See grounding.go.
//
// ggcode already had runtime metacognition (trajectory_health detectors that
// steer the live loop) and post-run qualitative reflection (run-insights
// memory). What was missing is the quantitative offline scorecard: given the
// session's full message log, quantify duplicate calls, failed calls, and the
// context tax they impose, then emit a transparent efficiency score plus
// actionable findings. runeval is that scorecard — pure analysis, no LLM
// calls, no runtime steering (it is not a detector; the user invokes it via
// the /runreport slash command).
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

	// Grounding axis (see grounding.go): file paths cited in the final
	// assistant answer, and how many were never opened by file tools nor
	// surfaced in any tool result of this run.
	CitedPaths          int
	UngroundedCount     int
	UngroundedCitations []string

	// EfficiencyScore is 100 minus transparent penalties (see Evaluate).
	// 0-100; 100 = clean trajectory.
	EfficiencyScore int

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

// resultObs is one ordered tool_result observation; wasted bytes are
// decided against record state learned during the same walk.
type resultObs struct {
	key     callKey
	isError bool
	outLen  int
}

// trajectoryWalk is the raw call/result structure collected from one
// message log.
type trajectoryWalk struct {
	byID       map[string]*callKey
	records    map[callKey]*callRecord
	keyOrder   []callKey
	results    []resultObs
	toolCalls  int
	toolErrors int
}

// walkTrajectory collects the call/result structure of the message log.
func walkTrajectory(msgs []provider.Message) trajectoryWalk {
	w := trajectoryWalk{
		byID:    map[string]*callKey{},
		records: map[callKey]*callRecord{},
	}
	for i := range msgs {
		for _, b := range msgs[i].Content {
			switch b.Type {
			case "tool_use":
				w.addCall(b)
			case "tool_result":
				w.addResult(b)
			}
		}
	}
	return w
}

func (w *trajectoryWalk) addCall(b provider.ContentBlock) {
	key := callKey{
		tool:  b.ToolName,
		input: canonicalJSON(b.Input),
	}
	w.byID[b.ToolID] = &key
	rec := w.records[key]
	if rec == nil {
		rec = &callRecord{
			tool:      key.tool,
			canonical: key.input,
			hashes:    map[string]bool{},
			readOnly:  readOnlyTools[key.tool],
		}
		w.records[key] = rec
		w.keyOrder = append(w.keyOrder, key)
	}
	rec.count++
	w.toolCalls++
}

func (w *trajectoryWalk) addResult(b provider.ContentBlock) {
	key := w.byID[b.ToolID]
	if key == nil {
		return // unpaired result (e.g. truncated log) — skip
	}
	rec := w.records[*key]
	if rec == nil {
		return
	}
	rec.hashes[truncateHash(b.Output)] = true
	if b.IsError {
		w.toolErrors++
	}
	w.results = append(w.results, resultObs{
		key:     *key,
		isError: b.IsError,
		outLen:  len(b.Output),
	})
}

// countTurns counts assistant messages that issued at least one tool call.
func countTurns(msgs []provider.Message) int {
	n := 0
	for i := range msgs {
		if msgs[i].Role != "assistant" {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" {
				n++
				break
			}
		}
	}
	return n
}

// wastedRepeats counts superseded results that added no information: the
// final result of a call key is the informative one — the final state the
// agent actually learned from; earlier results are wasted when they
// failed (no usable information) or when they are identical read-only
// repeats (duplicate of what the last call returns anyway).
func wastedRepeats(records map[callKey]*callRecord, results []resultObs) (calls, bytes int) {
	lastIdx := make(map[callKey]int, len(results))
	for i, res := range results {
		lastIdx[res.key] = i
	}
	for i, res := range results {
		rec := records[res.key]
		if rec == nil || i == lastIdx[res.key] {
			continue
		}
		if res.isError || (rec.readOnly && rec.identical()) {
			calls++
			bytes += res.outLen
		}
	}
	return calls, bytes
}

// duplicateGroups summarizes keys invoked more than once, worst first.
func duplicateGroups(records map[callKey]*callRecord, keyOrder []callKey) []DuplicateGroup {
	var groups []DuplicateGroup
	for _, k := range keyOrder {
		rec := records[k]
		if rec.count < 2 {
			continue
		}
		groups = append(groups, DuplicateGroup{
			Tool:      rec.tool,
			Input:     truncateDisplay(rec.canonical),
			Count:     rec.count,
			Repeats:   rec.count - 1,
			Identical: rec.identical(),
			ReadOnly:  rec.readOnly,
		})
	}
	sort.Slice(groups, func(a, b int) bool {
		if groups[a].Repeats != groups[b].Repeats {
			return groups[a].Repeats > groups[b].Repeats
		}
		return groups[a].Tool < groups[b].Tool
	})
	return groups
}

// Evaluate walks the full message log and produces a Report. It is pure:
// same input, same output, no side effects, no LLM calls.
func Evaluate(msgs []provider.Message, usage []UsageSample) Report {
	var r Report

	w := walkTrajectory(msgs)
	r.ToolCalls = w.toolCalls
	r.ToolErrors = w.toolErrors

	// Distinct tools.
	seenTools := map[string]bool{}
	for _, k := range w.keyOrder {
		seenTools[w.records[k].tool] = true
	}
	r.DistinctTools = len(seenTools)

	r.TurnCount = countTurns(msgs)

	r.WastedRepeatCalls, r.WastedResultBytes = wastedRepeats(w.records, w.results)
	r.DuplicateGroups = duplicateGroups(w.records, w.keyOrder)

	r.WastedTokenEstimate = r.WastedResultBytes / bytesPerToken

	// Usage split.
	for _, s := range usage {
		t := s.Usage.Total()
		r.TotalTokens += t
		if s.Source != "" && s.Source != "agent" {
			r.OverheadTokens += t
		}
	}

	// Grounding axis (see grounding.go): deterministic citation check.
	applyGrounding(&r, msgs)

	r.EfficiencyScore = score(r)
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

// findings renders worst-first, quantified improvement hints.
func findings(r Report) []string {
	var f []string

	if r.UngroundedCount > 0 {
		f = append(f, fmt.Sprintf(
			"%d of %d file paths cited in the answer never appear in this run's tool trace (%s) — ungrounded citations; verify these claims are evidence-backed",
			r.UngroundedCount, r.CitedPaths, strings.Join(r.UngroundedCitations, ", ")))
	}

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

	return f
}

// Render formats the report as a compact scorecard for chat display.
func Render(r Report) string {
	var b strings.Builder
	grade := "good"
	switch {
	case r.EfficiencyScore >= 90:
		grade = "excellent"
	case r.EfficiencyScore < 60:
		grade = "needs work"
	}
	fmt.Fprintf(&b, "Run report — trajectory scorecard (efficiency %d/100, %s)\n", r.EfficiencyScore, grade)
	fmt.Fprintf(&b, "  Steps: %d turns with tool calls · %d tool calls · %d distinct tools\n",
		r.TurnCount, r.ToolCalls, r.DistinctTools)
	fmt.Fprintf(&b, "  Failures: %d/%d tool calls errored · wasted repeats: %d\n",
		r.ToolErrors, r.ToolCalls, r.WastedRepeatCalls)
	if r.CitedPaths > 0 {
		if r.UngroundedCount > 0 {
			fmt.Fprintf(&b, "  Grounding: %d/%d cited paths verified in trace · ungrounded: %s\n",
				r.CitedPaths-r.UngroundedCount, r.CitedPaths,
				strings.Join(r.UngroundedCitations, ", "))
		} else {
			fmt.Fprintf(&b, "  Grounding: all %d cited paths verified in tool trace\n", r.CitedPaths)
		}
	}
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
