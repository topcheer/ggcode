// Package tapeeval grades recorded tool tapes against a declarative eval
// spec — the offline half of eval-driven development for ggcode.
//
// Background: Anthropic's engineering guidance ("Demystifying evals for AI
// agents", 2025, https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)
// and the evaluation-driven-development process model (arXiv:2411.13768)
// converge on the same core loop: record real agent transcripts, encode the
// behaviors worth protecting as assertions ("code-based graders"), and
// re-run those evals on every change to catch regressions before users do.
// ggcode already records transcripts — every tool execution is persisted to
// a tape via GGCODE_TOOL_TAPE=record:<path> (internal/agent/tool_tape.go) —
// and can replay them, but a tape could not be GRADED: there was no way to
// say "this trajectory must use read_file before edit_file, must never call
// run_command, and must finish in <= 40 tool calls". This package is that
// grader.
//
// Design constraints:
//   - Deterministic and offline: pure functions over the recorded entries;
//     no LLM, no network, no filesystem side effects. Suitable for CI.
//   - Code-based graders only, per the same guidance: tool-call
//     verification (required/forbidden/ordering) and transcript analysis
//     (call budget, error budget). Model-based graders are deliberately out
//     of scope.
//   - Scaffold-to-regression flow: `ggcode eval init` derives a baseline
//     spec from a recorded tape (passing by construction on its source
//     tape); the operator tightens it by hand. This mirrors the guidance
//     that capability evals "graduate" into regression evals.
package tapeeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/toolreplay"
)

// SpecVersion is the current eval spec schema version.
const SpecVersion = 1

// Spec is a declarative eval for a recorded tool tape (JSON file). Every
// assertion is optional; an empty spec evaluates to a pass with metrics
// only, which is still useful for tracking transcript shape over time.
type Spec struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	// Description is free-form documentation surfaced in reports.
	Description string `json:"description,omitempty"`
	// RequiredTools: each assertion's tool must appear at least MinCount
	// times (default 1), optionally filtered by InputRegex matched against
	// the raw JSON input of the call.
	RequiredTools []ToolAssertion `json:"required_tools,omitempty"`
	// ForbiddenTools: these tools must not be called at all.
	ForbiddenTools []string `json:"forbidden_tools,omitempty"`
	// ToolOrder: the recorded tool-name sequence must contain these names
	// as a subsequence (in order, gaps allowed). Single-element entries are
	// equivalent to a required tool without a count.
	ToolOrder []string `json:"tool_order,omitempty"`
	// MaxToolCalls: transcript budget — total recorded calls must not
	// exceed this. Ignored when 0.
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// MaxErrorResults: error budget — tool results flagged as errors (or
	// carrying a Go-level error) must not exceed this. Ignored when 0.
	MaxErrorResults int `json:"max_error_results,omitempty"`
}

// ToolAssertion requires a tool to be used a minimum number of times,
// optionally constrained to calls whose raw input JSON matches InputRegex.
type ToolAssertion struct {
	Tool string `json:"tool"`
	// InputRegex is an optional regular expression applied to the raw
	// input JSON of each call with this tool name.
	InputRegex string `json:"input_regex,omitempty"`
	// MinCount is the minimum number of matching calls (default 1).
	MinCount int `json:"min_count,omitempty"`
}

// Metrics summarizes the transcript shape of the graded tape. Computed for
// every evaluation regardless of which assertions are configured, so a spec
// with zero assertions still yields comparable regression data.
type Metrics struct {
	ToolCalls     int            `json:"tool_calls"`
	ErrorCalls    int            `json:"error_calls"`
	DistinctTools int            `json:"distinct_tools"`
	ToolCounts    map[string]int `json:"tool_counts"`
}

// Result is the outcome of a single check.
type Result struct {
	Check string `json:"check"`
	Pass  bool   `json:"pass"`
	// Detail is a human-readable explanation (always populated, on pass
	// and on failure, so reports explain themselves).
	Detail string `json:"detail"`
}

// Report is the full grading outcome for one tape against one spec.
type Report struct {
	Spec    string   `json:"spec"`
	Pass    bool     `json:"pass"`
	Entries int      `json:"entries"`
	Metrics Metrics  `json:"metrics"`
	Results []Result `json:"results"`
}

// Passed returns the number of checks that passed.
func (r *Report) Passed() int {
	n := 0
	for _, res := range r.Results {
		if res.Pass {
			n++
		}
	}
	return n
}

// Evaluate grades the recorded entries against the spec. It never returns
// an error: malformed assertions (e.g. an invalid InputRegex) become
// failing checks with an explanatory detail, so a broken spec is visible in
// the report rather than aborting the whole eval run.
func Evaluate(entries []toolreplay.Entry, spec *Spec) *Report {
	rep := &Report{
		Spec:    spec.Name,
		Entries: len(entries),
		Metrics: Metrics{ToolCounts: make(map[string]int)},
	}

	tools := make([]string, 0, len(entries))
	for _, e := range entries {
		tools = append(tools, e.ToolName)
		rep.Metrics.ToolCalls++
		rep.Metrics.ToolCounts[e.ToolName]++
		if e.Result.IsError || e.Err != "" {
			rep.Metrics.ErrorCalls++
		}
	}
	rep.Metrics.DistinctTools = len(rep.Metrics.ToolCounts)

	rep.Results = append(rep.Results, checkRequiredTools(entries, spec.RequiredTools)...)
	rep.Results = append(rep.Results, checkForbiddenTools(tools, spec.ForbiddenTools)...)
	if len(spec.ToolOrder) > 0 {
		rep.Results = append(rep.Results, checkToolOrder(tools, spec.ToolOrder))
	}
	rep.Results = append(rep.Results, checkBudgets(rep.Metrics, spec)...)

	rep.Pass = rep.Passed() == len(rep.Results)
	return rep
}

// checkRequiredTools verifies each tool assertion independently.
func checkRequiredTools(entries []toolreplay.Entry, reqs []ToolAssertion) []Result {
	results := make([]Result, 0, len(reqs))
	for _, req := range reqs {
		name := fmt.Sprintf("required_tools[%s]", req.Tool)
		if req.InputRegex != "" {
			name += " ~" + req.InputRegex
		}
		if req.InputRegex == "" {
			// Distinguish duplicate assertions on the same tool.
			if n := countName(results, name); n > 0 {
				name = fmt.Sprintf("%s#%d", name, n)
			}
		}
		min := req.MinCount
		if min <= 0 {
			min = 1
		}

		var re *regexp.Regexp
		if req.InputRegex != "" {
			var err error
			re, err = regexp.Compile(req.InputRegex)
			if err != nil {
				results = append(results, Result{
					Check:  name,
					Pass:   false,
					Detail: fmt.Sprintf("invalid input_regex: %v", err),
				})
				continue
			}
		}

		matches := 0
		for _, e := range entries {
			if e.ToolName != req.Tool {
				continue
			}
			if re != nil && !re.Match(e.Input) {
				continue
			}
			matches++
		}
		results = append(results, Result{
			Check:  name,
			Pass:   matches >= min,
			Detail: fmt.Sprintf("%d matching call(s), need >= %d", matches, min),
		})
	}
	return results
}

func countName(results []Result, name string) int {
	n := 0
	for _, r := range results {
		if r.Check == name {
			n++
		}
	}
	return n
}

// checkForbiddenTools fails listing every offending call position.
func checkForbiddenTools(tools []string, forbidden []string) []Result {
	results := make([]Result, 0, len(forbidden))
	for _, tool := range forbidden {
		var positions []int
		for i, t := range tools {
			if t == tool {
				positions = append(positions, i)
			}
		}
		res := Result{Check: fmt.Sprintf("forbidden_tools[%s]", tool)}
		if len(positions) > 0 {
			res.Pass = false
			res.Detail = fmt.Sprintf("%d forbidden call(s) at position(s) %v", len(positions), positions)
		} else {
			res.Pass = true
			res.Detail = "not called"
		}
		results = append(results, res)
	}
	return results
}

// checkToolOrder verifies the subsequence property and reports the matched
// positions so failures show how far the order drifted.
func checkToolOrder(tools, order []string) Result {
	res := Result{Check: "tool_order"}
	if len(order) == 0 {
		res.Pass = true
		res.Detail = "no order asserted"
		return res
	}
	pos := make([]int, 0, len(order))
	j := 0
	for i, t := range tools {
		if j < len(order) && t == order[j] {
			pos = append(pos, i)
			j++
		}
	}
	if j == len(order) {
		res.Pass = true
		res.Detail = fmt.Sprintf("%v found in order at positions %v", order, pos)
	} else {
		res.Pass = false
		res.Detail = fmt.Sprintf("matched %d/%d step(s); %q never follows %q",
			j, len(order), order[j], orderBefore(order, j))
	}
	return res
}

func orderBefore(order []string, j int) string {
	if j == 0 {
		return "<start>"
	}
	return order[j-1]
}

// checkBudgets enforces the transcript call budget and error budget.
func checkBudgets(m Metrics, spec *Spec) []Result {
	results := make([]Result, 0, 2)
	if spec.MaxToolCalls > 0 {
		results = append(results, Result{
			Check:  "max_tool_calls",
			Pass:   m.ToolCalls <= spec.MaxToolCalls,
			Detail: fmt.Sprintf("%d call(s), budget <= %d", m.ToolCalls, spec.MaxToolCalls),
		})
	}
	if spec.MaxErrorResults > 0 {
		results = append(results, Result{
			Check:  "max_error_results",
			Pass:   m.ErrorCalls <= spec.MaxErrorResults,
			Detail: fmt.Sprintf("%d error result(s), budget <= %d", m.ErrorCalls, spec.MaxErrorResults),
		})
	}
	return results
}

// LoadSpec reads and validates an eval spec from a JSON file. Unknown
// fields are rejected so a typo (e.g. "require_tool") fails loudly instead
// of silently grading an empty spec.
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval spec: %w", err)
	}
	var spec Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse eval spec %s: %w", path, err)
	}
	if spec.Version == 0 {
		spec.Version = SpecVersion
	}
	if spec.Version != SpecVersion {
		return nil, fmt.Errorf("eval spec %s: version %d unsupported (want %d)", path, spec.Version, SpecVersion)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("eval spec %s: missing required field \"name\"", path)
	}
	return &spec, nil
}

// Scaffold derives a baseline eval spec from a recorded tape. The scaffold
// passes on its own source tape by construction: observed per-tool call
// counts become required minimums, the call total becomes the call budget,
// and the observed error count becomes the error budget. Tighten by hand
// afterwards — drop brittle per-tool minimums, shrink budgets, add
// input_regex anchors. A scaffold is a starting point for a regression
// eval, not a finished one.
func Scaffold(entries []toolreplay.Entry, name string) *Spec {
	counts := make(map[string]int)
	errCalls := 0
	var seen []string // first-appearance order, then sorted for stability
	for _, e := range entries {
		if _, ok := counts[e.ToolName]; !ok {
			seen = append(seen, e.ToolName)
		}
		counts[e.ToolName]++
		if e.Result.IsError || e.Err != "" {
			errCalls++
		}
	}
	sort.Strings(seen)

	spec := &Spec{Version: SpecVersion, Name: name}
	for _, tool := range seen {
		spec.RequiredTools = append(spec.RequiredTools, ToolAssertion{
			Tool:     tool,
			MinCount: counts[tool],
		})
	}
	spec.MaxToolCalls = len(entries)
	spec.MaxErrorResults = errCalls
	return spec
}

// FormatReport renders a human-readable grading report.
func FormatReport(r *Report, tapePath, specPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tape eval %q\n", r.Spec)
	fmt.Fprintf(&b, "  tape: %s (%d tool calls, %d error results, %d distinct tools)\n",
		tapePath, r.Metrics.ToolCalls, r.Metrics.ErrorCalls, r.Metrics.DistinctTools)
	fmt.Fprintf(&b, "  spec: %s\n", specPath)
	if len(r.Results) == 0 {
		b.WriteString("  checks: none (spec has no assertions)\n")
	}
	for _, res := range r.Results {
		status := "PASS"
		if !res.Pass {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "  %s %s\n      %s\n", status, res.Check, res.Detail)
	}
	verdict := "PASS"
	if !r.Pass {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "  result: %s (%d/%d checks passed)\n", verdict, r.Passed(), len(r.Results))
	return b.String()
}
