package agent

import (
	"encoding/json"
	"strings"
	"sync"
)

// pivotDecisionTracker implements the Pivot/Refine meta-decision from
// AutoResearchClaw (2026, one of the year's top agentic papers): a failed
// experiment is information. Before retrying, the system must explicitly
// decide between REPAIR (keep the current approach, fix the concrete cause)
// and PIVOT (abandon the approach, name a different one) - instead of
// silently continuing incremental retries on a dead path.
//
// Existing detectors cover the neighbors but not this decision point:
//   - solution_fixation: failed EDIT clusters on one file (anchor refresh)
//   - strategy_exhaustion: fires AFTER 4 different recovery strategies all
//     failed - i.e. after over-pivoting, not before a retry
//   - capability_boundary: detects too MANY pivots (flailing)
//
// None of them asks the model to make an explicit repair-vs-pivot call.
// This tracker does, keyed on consecutive failures of the same shell
// command family (run_command), e.g. "go test" failing 3 times in a row.
//
// Deterministic (zero LLM cost): counting and key extraction are pure.
type pivotDecisionTracker struct {
	mu sync.Mutex
	// fails maps a command key ("go test") to its consecutive failure
	// count. A success of the same key clears the entry; successes of
	// other commands do NOT (a passing `ls` says nothing about `go test`).
	fails map[string]int
	// warned maps a key to how many decision prompts it has emitted this
	// run (max 2: first REPAIR-or-PIVOT, then forced PIVOT).
	warned map[string]int
}

const (
	// pivotFirstThreshold: consecutive failures of one command family that
	// trigger the first explicit REPAIR-vs-PIVOT decision prompt. Aligned
	// with solution_fixation's 3-failure window.
	pivotFirstThreshold = 3
	// pivotForcedThreshold: at this many consecutive failures the model has
	// implicitly chosen REPAIR (it kept the path) and been wrong again -
	// the prompt escalates to forced PIVOT with an abandon rationale.
	pivotForcedThreshold = 6
	// pivotMaxWarningsPerKey caps emissions per command family per run.
	pivotMaxWarningsPerKey = 2
)

func newPivotDecisionTracker() *pivotDecisionTracker {
	return &pivotDecisionTracker{
		fails:  make(map[string]int),
		warned: make(map[string]int),
	}
}

// reset clears all per-run state (new user turn).
func (p *pivotDecisionTracker) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fails = make(map[string]int)
	p.warned = make(map[string]int)
}

// pivotCommandArgs mirrors the run_command argument schema.
type pivotCommandArgs struct {
	Command string `json:"command"`
}

// recordToolCall observes every tool result. Only run_command participates:
// verify/build failures are the canonical "experiment failed" signal; other
// tools (edits, reads) have their own detectors.
// #3089 V2: environmental failures (timeout, OOM/signal kill) say nothing
// about the strategy itself - they are neutral and do not touch the streak
// in either direction.
func (p *pivotDecisionTracker) recordToolCall(toolName, args string, isError bool, resultContent string) {
	if p == nil {
		return
	}
	key := pivotCommandKey(toolName, args)
	if key == "" {
		return
	}
	if isError && pivotEnvironmentalFailure(resultContent) {
		return // environment noise: neither counts as failure nor as success
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !isError {
		delete(p.fails, key) // same-family success: path works again
		return
	}
	p.fails[key]++
}

// checkAndWarn returns a structured decision prompt when a command family
// has failed enough consecutive times, or "" otherwise.
func (p *pivotDecisionTracker) checkAndWarn() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, n := range p.fails {
		if p.warned[key] >= pivotMaxWarningsPerKey {
			continue
		}
		if n < pivotFirstThreshold {
			continue
		}
		if n >= pivotForcedThreshold && p.warned[key] >= 1 {
			// Second emission: the model kept the path (implicit REPAIR)
			// and failed again. Force the pivot.
			p.warned[key]++
			delete(p.fails, key) // reset streak; further failures restart counting
			return "[pivot-decision] The command family `" + key + "` has now failed " +
				itoaPivot(n) + " consecutive times. Continuing to repair this exact path is no longer credible. " +
				"PIVOT: state in one line why this approach is failing, then name and switch to a DIFFERENT approach " +
				"(different tool, different scope, or ask the user). Do not run `" + key + "` again without that pivot statement."
		}
		if p.warned[key] == 0 {
			p.warned[key]++
			return "[pivot-decision] The command family `" + key + "` has failed " +
				itoaPivot(n) + " consecutive times. Before the next retry, make an explicit decision and state it in one line:\n" +
				"- REPAIR: keep this approach. Say why it is still viable and what concretely changes in the next attempt.\n" +
				"- PIVOT: abandon it. Name the different approach you switch to instead.\n" +
				"Silent incremental retries of the same command are the failure mode this interrupt exists to break."
		}
	}
	return ""
}

// pivotCommandKey extracts a stable "command family" key from a run_command
// invocation: the first two whitespace-separated tokens of the main command
// (e.g. "go test", "make verify", "npm run"). Argument churn (paths, flags)
// does not fragment the family. Returns "" for other tools or empty commands.
//
// #3089 V1: shell plumbing must not become the family key. `set -e` lines,
// leading `cd x &&` segments, `sudo`/`env` wrappers and `VAR=value`
// assignments are stripped so `cd /x && go test` buckets with `go test`,
// and `set -e` scripts no longer all collapse into one bogus family.
func pivotCommandKey(toolName, args string) string {
	if toolName != "run_command" {
		return ""
	}
	var ca pivotCommandArgs
	if err := json.Unmarshal([]byte(args), &ca); err != nil || ca.Command == "" {
		return ""
	}
	for _, line := range strings.Split(ca.Command, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key := pivotMainSegment(line); key != "" {
			return key
		}
	}
	return ""
}

// pivotMainSegment walks a command line (possibly `cd x && payload` or with
// `sudo`/`env`/`VAR=` prefixes) and returns the two-token family key of the
// first segment that is real payload rather than shell plumbing.
func pivotMainSegment(line string) string {
	for _, seg := range strings.Split(line, "&&") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		// Drop leading wrapper tokens: sudo / env / VAR=value assignments.
		i := 0
		for i < len(fields) && (fields[i] == "sudo" || fields[i] == "env" || pivotIsEnvAssign(fields[i])) {
			i++
		}
		fields = fields[i:]
		if len(fields) == 0 {
			continue
		}
		// `cd x` and `set -e` are plumbing, not the payload: skip the segment.
		if fields[0] == "cd" || fields[0] == "set" {
			continue
		}
		if len(fields) >= 2 {
			return fields[0] + " " + fields[1]
		}
		return fields[0]
	}
	return ""
}

// pivotIsEnvAssign reports whether tok is a `NAME=value` shell assignment
// (flags like `--foo=bar` are not).
func pivotIsEnvAssign(tok string) bool {
	i := strings.Index(tok, "=")
	if i <= 0 {
		return false
	}
	for _, c := range tok[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// pivotEnvironmentalFailure reports whether a failed run_command result was
// caused by the environment rather than the command's own strategy (#3089 V2).
func pivotEnvironmentalFailure(content string) bool {
	if content == "" {
		return false
	}
	env := []string{
		"signal: killed",
		"signal: terminated",
		"context deadline exceeded",
		"command timed out",
		"i/o timeout",
		"exit status 137",
		"exit status 124",
		"exit code 137",
		"exit code 124",
	}
	for _, m := range env {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

// itoaPivot avoids importing strconv for a single call site.
func itoaPivot(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
