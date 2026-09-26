// Package replay implements deterministic replay of a recorded agent
// session's tool calls (frontier concept: "Deterministic Replay for AI Agent
// Systems", arXiv:2607.16200 / AgentReplay, 2026).
//
// An agent run couples LLM sampling with external tool effects, so a past
// run can never be re-executed bit-for-bit once the model is in the loop.
// This package takes the pragmatic slice the paper and the AgentReplay /
// reaatech agent-replay projects call "replay with stubs": the recorded
// session JSONL already contains every tool_use block (name + input) and its
// recorded tool_result, so we can re-execute the *tool calls only* -- zero
// LLM inference, zero tokens -- against the current workspace and diff the
// live results against the recorded ones. That answers two practical
// questions without re-running the agent:
//
//   - "What exactly did this run do?" (inspectable, ordered trace)
//   - "Which of its observations have drifted?" (e.g. after a partial fix,
//     do the recorded test failures still reproduce?)
//
// Only a conservative allowlist of read-only tools is ever re-executed;
// mutating tools are reported as skipped by design so replay can never
// rewrite the workspace it is inspecting.
package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// DefaultAllowlist is the set of built-in tools that are safe to
// re-execute during replay: strictly read-only, no network, no wall-clock
// dependence. Names not present in the active registry are inert.
var DefaultAllowlist = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"grep":            true,
	"glob":            true,
	"list_directory":  true,
	"search_files":    true,
	"code_search":     true,
	"code_health":     true,
}

// TraceStep is one recorded tool call paired with its recorded result.
type TraceStep struct {
	Index           int             `json:"index"` // 1-based position in the trace
	ToolName        string          `json:"tool_name"`
	ToolID          string          `json:"tool_id"`
	Input           json.RawMessage `json:"input,omitempty"`
	RecordedOutput  string          `json:"recorded_output,omitempty"`
	RecordedIsError bool            `json:"recorded_is_error,omitempty"`
	Unpaired        bool            `json:"unpaired,omitempty"` // tool_use without a recorded result
}

// Status classifies the outcome of replaying one trace step.
type Status string

const (
	StatusMatch           Status = "match"         // live result equals recorded (noise-normalized)
	StatusDrift           Status = "drift"         // live result differs from recorded
	StatusSkipMutating    Status = "skip-mutating" // tool is not on the read-only allowlist
	StatusSkipMissingTool Status = "skip-missing"  // tool not registered in this build
	StatusSkipUnpaired    Status = "skip-unpaired" // recorded call has no recorded result
	StatusExecError       Status = "exec-error"    // live execution itself failed
)

// StepResult is the replay verdict for a single trace step.
type StepResult struct {
	Step          TraceStep `json:"step"`
	Status        Status    `json:"status"`
	AddedLines    int       `json:"added_lines,omitempty"`     // lines live has that recorded lacks
	RemovedLines  int       `json:"removed_lines,omitempty"`   // lines recorded has that live lacks
	FirstDiffLine int       `json:"first_diff_line,omitempty"` // 1-based first divergent line, 0 = n/a
	LiveOutput    string    `json:"live_output,omitempty"`
	Err           string    `json:"err,omitempty"`
}

// Report aggregates replay results for a session.
type Report struct {
	SessionID string       `json:"session_id"`
	Steps     []StepResult `json:"steps"`
}

// DriftDetected reports whether any step produced a live result that
// differs from the recorded one (including a flipped error flag).
func (r *Report) DriftDetected() bool {
	for _, s := range r.Steps {
		if s.Status == StatusDrift || s.Status == StatusExecError {
			return true
		}
	}
	return false
}

// Counts returns (total, match, drift, skipped) totals.
func (r *Report) Counts() (total, match, drift, skipped int) {
	for _, s := range r.Steps {
		total++
		switch s.Status {
		case StatusMatch:
			match++
		case StatusDrift, StatusExecError:
			drift++
		default:
			skipped++
		}
	}
	return
}

// ExtractTrace walks session messages in order and pairs every tool_use
// block with its tool_result (matched by tool_id). The returned steps are
// in call order, which is the order replay executes them in.
func ExtractTrace(msgs []provider.Message) []TraceStep {
	results := make(map[string]provider.ContentBlock)
	// Record result blocks first is wrong -- results follow uses in order.
	// Walk twice: first collect results, then collect uses in order.
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type == "tool_result" && b.ToolID != "" {
				results[b.ToolID] = b
			}
		}
	}
	var steps []TraceStep
	idx := 0
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type != "tool_use" || b.ToolName == "" {
				continue
			}
			idx++
			step := TraceStep{
				Index:    idx,
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
			}
			if res, ok := results[b.ToolID]; ok {
				step.RecordedOutput = res.Output
				step.RecordedIsError = res.IsError
			} else {
				step.Unpaired = true
			}
			steps = append(steps, step)
		}
	}
	return steps
}

// Replayer re-executes allowlisted read-only tool calls from a trace.
type Replayer struct {
	Registry     *tool.Registry
	AllowedTools map[string]bool
	PerStepTime  time.Duration
}

// NewReplayer builds a replayer using the read-only DefaultAllowlist.
func NewReplayer(reg *tool.Registry) *Replayer {
	return &Replayer{
		Registry:     reg,
		AllowedTools: DefaultAllowlist,
		PerStepTime:  30 * time.Second,
	}
}

// Run replays every trace step and returns the aggregated report.
// It never mutates the workspace: non-allowlisted tools are skipped.
func (rp *Replayer) Run(ctx context.Context, sessionID string, trace []TraceStep) *Report {
	rep := &Report{SessionID: sessionID}
	for _, step := range trace {
		rep.Steps = append(rep.Steps, rp.replayStep(ctx, step))
	}
	return rep
}

func (rp *Replayer) replayStep(ctx context.Context, step TraceStep) StepResult {
	res := StepResult{Step: step}
	if step.Unpaired {
		res.Status = StatusSkipUnpaired
		return res
	}
	allowed := rp.AllowedTools == nil || rp.AllowedTools[step.ToolName]
	t, ok := rp.Registry.Get(step.ToolName)
	if !ok {
		if allowed {
			res.Status = StatusSkipMissingTool
		} else {
			res.Status = StatusSkipMutating
		}
		return res
	}
	if !allowed {
		res.Status = StatusSkipMutating
		return res
	}
	stepCtx := ctx
	cancel := func() {}
	if rp.PerStepTime > 0 {
		stepCtx, cancel = context.WithTimeout(ctx, rp.PerStepTime)
	}
	defer cancel()

	out, err := t.Execute(stepCtx, step.Input)
	if err != nil {
		res.Status = StatusExecError
		res.Err = err.Error()
		return res
	}
	res.LiveOutput = out.Content
	if classifyErrorDrift(step, out) {
		res.Status = StatusDrift
		return res
	}
	added, removed, first := diffStats(step.RecordedOutput, out.Content)
	res.AddedLines, res.RemovedLines, res.FirstDiffLine = added, removed, first
	if added == 0 && removed == 0 {
		res.Status = StatusMatch
	} else {
		res.Status = StatusDrift
	}
	return res
}

// classifyErrorDrift flags a flipped error flag as drift regardless of text.
func classifyErrorDrift(step TraceStep, out tool.Result) bool {
	return step.RecordedIsError != out.IsError
}

// normalize applies the paper's "noise tier": strip CR, trailing
// whitespace per line, and trailing blank lines, so cosmetic deltas do not
// count as drift.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// diffStats compares normalized outputs and returns added/removed line
// counts plus the 1-based first divergent line (0 when equal). It uses a
// line-multiset comparison: O(n), deterministic, and good enough as a drift
// signal (replay is a verdict, not a patch).
func diffStats(recorded, live string) (added, removed, firstDiff int) {
	rn, ln := strings.Split(normalize(recorded), "\n"), strings.Split(normalize(live), "\n")
	// Trim trailing empty lines of both.
	rn = trimEmpty(rn)
	ln = trimEmpty(ln)
	rcount, lcount := make(map[string]int, len(rn)), make(map[string]int, len(ln))
	for _, l := range rn {
		rcount[l]++
	}
	for _, l := range ln {
		lcount[l]++
	}
	for l, c := range rcount {
		if d := c - lcount[l]; d > 0 {
			removed += d
		}
	}
	for l, c := range lcount {
		if d := c - rcount[l]; d > 0 {
			added += d
		}
	}
	if added == 0 && removed == 0 {
		return 0, 0, 0
	}
	n := len(rn)
	if len(ln) < n {
		n = len(ln)
	}
	for i := 0; i < n; i++ {
		if rn[i] != ln[i] {
			return added, removed, i + 1
		}
	}
	return added, removed, n + 1
}

func trimEmpty(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Render writes a human-readable replay report.
func (r *Report) Render(w io.Writer) {
	total, match, drift, skipped := r.Counts()
	fmt.Fprintf(w, "replay %s: %d steps -- %d match, %d drift, %d skipped\n", r.SessionID, total, match, drift, skipped)
	for _, s := range r.Steps {
		argHint := argHint(s.Step.Input)
		switch s.Status {
		case StatusMatch:
			fmt.Fprintf(w, "[%3d] %-16s MATCH    %s\n", s.Step.Index, s.Step.ToolName, argHint)
		case StatusDrift:
			fmt.Fprintf(w, "[%3d] %-16s DRIFT    %s  (+%d/-%d lines, first diff: line %d)\n",
				s.Step.Index, s.Step.ToolName, argHint, s.AddedLines, s.RemovedLines, s.FirstDiffLine)
		case StatusExecError:
			fmt.Fprintf(w, "[%3d] %-16s EXEC-ERR %s  (%s)\n", s.Step.Index, s.Step.ToolName, argHint, truncate(s.Err, 120))
		case StatusSkipMutating:
			fmt.Fprintf(w, "[%3d] %-16s SKIP     %s  (mutating tool, not replayed)\n", s.Step.Index, s.Step.ToolName, argHint)
		case StatusSkipMissingTool:
			fmt.Fprintf(w, "[%3d] %-16s SKIP     %s  (tool not registered)\n", s.Step.Index, s.Step.ToolName, argHint)
		case StatusSkipUnpaired:
			fmt.Fprintf(w, "[%3d] %-16s SKIP     %s  (no recorded result)\n", s.Step.Index, s.Step.ToolName, argHint)
		}
	}
}

// argHint extracts a short identifying fragment from a tool call's input
// (path, pattern, query -- whatever key comes first alphabetically that is a
// string), for stable one-line summaries.
func argHint(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, truncate(s, 60)))
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
