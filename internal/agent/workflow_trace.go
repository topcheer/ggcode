package agent

// sa-85 (SkillTracer-inspired, KDD 2026 DOI 10.1145/3770855.3817981):
// structural failure attribution for workflow steps.
//
// Frontier claim: long-horizon agents "fail without knowing where or why
// execution broke down" - when a guarded command is rejected for an unmet
// prerequisite, a bare "missing=X" cannot distinguish (a) the prerequisite's
// command never ran, (b) it ran and errored, (c) it ran fine but produced no
// artifact. SkillTracer attributes failures to plan-graph NODES with local
// repair instead of whole-workflow retry (+17.7% long-horizon success).
//
// Go-native subset: the workflow engine's step graph (r26) gains a failure
// ledger. Every executed command matching a step's OnCommands records a
// StepAttempt (outcome + artifact grounding). When checkPreconditions builds
// a violation, the counterexample carries the prerequisite's recent attempts
// and the rejection message names WHERE it broke and the local repair -
// deterministically, zero LLM cost. The success-side registry (r26
// recordCompletion) is untouched: this file adds only the missing failure
// side.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// wfTraceWindow caps retained attempts (sliding; enough for attribution
// without unbounded memory on long runs).
const wfTraceWindow = 30

// isCommandExecTool reports whether the tool executes a shell command whose
// args carry a "command" field both the precondition check and the attempt
// ledger must see. run_command is the foreground path; start_command runs
// the same command in the background (#3836 A: gating on run_command alone
// let a guarded command skip block prechecks and the ledger entirely just
// by being launched in the background - the step then reported
// "never attempted" about a command that had actually run).
func isCommandExecTool(toolName string) bool {
	return toolName == "run_command" || toolName == "start_command"
}

// wfTraceMaxPerViolation caps attempts attached to one violation message.
const wfTraceMaxPerViolation = 3

// wfErrSnippetMax bounds the error text carried per attempt.
const wfErrSnippetMax = 160

// StepAttempt is one executed command that matched a step's OnCommands -
// the node-level trace record SkillTracer derives attribution from.
type StepAttempt struct {
	StepID           string    `json:"step_id"`
	Command          string    `json:"command"`
	IsError          bool      `json:"is_error"`
	ErrSnippet       string    `json:"err_snippet,omitempty"`
	ProducedArtifact bool      `json:"produced_artifact"`
	At               time.Time `json:"at"`
}

// recordAttempt logs the outcome of a run_command that matched a guarded
// step. Called once per executed command (never for blocked calls - those
// never ran, so they are not attempts).
func (e *workflowEngine) recordAttempt(toolName string, args json.RawMessage, res tool.Result) {
	if e == nil || !isCommandExecTool(toolName) {
		return
	}
	e.loadWorkflowSpec()
	e.mu.RLock()
	command, _ := parseRunCommandArgs(args)
	// #3780 C: attribute the attempt to EVERY matching step, not just the
	// first. The old first-match break made the write path (recordCompletion
	// walks all steps) and the trace path asymmetric: with overlapping
	// OnCommands (A=`go test*`, B=`go test -run Build*`), executing B's
	// command recorded an attempt only under A; when B later became a
	// rejected prerequisite, wfAttemptAttribution asserted "step B was
	// never executed" about a command that had just run, and B's
	// ProducedArtifact flag was decided by A's artifact_glob.
	var hits []string
	grounded := map[string]bool{}
	if command != "" {
		for _, id := range e.order {
			st := e.steps[id]
			if !commandMatches(st.OnCommands, command) {
				continue
			}
			hits = append(hits, id)
			// Artifact grounding reuses the #3414 safety-net probe (walk is
			// done under RLock, same precedent as checkPreconditions -> groundIfFresh).
			if st.ArtifactGlob != "" && !res.IsError {
				grounded[id] = e.groundIfFresh(id)
			}
		}
	}
	e.mu.RUnlock()
	if len(hits) == 0 {
		return // command belongs to no guarded step: nothing to attribute
	}
	snip := res.Content
	if len(snip) > wfErrSnippetMax {
		snip = snip[:wfErrSnippetMax]
	}
	now := time.Now()
	e.traceMu.Lock()
	if e.attemptCounts == nil {
		e.attemptCounts = make(map[string]int)
	}
	for _, id := range hits {
		e.attemptCounts[id]++
		e.trace = append(e.trace, StepAttempt{
			StepID:           id,
			Command:          command,
			IsError:          res.IsError,
			ErrSnippet:       snip,
			ProducedArtifact: grounded[id],
			At:               now,
		})
	}
	if len(e.trace) > wfTraceWindow {
		e.trace = e.trace[len(e.trace)-wfTraceWindow:]
	}
	e.traceMu.Unlock()
}

// recentAttempts returns up to n NEWEST attempts recorded for stepID
// (oldest first, so callers taking att[len(att)-1] get the truly latest).
// Uses a dedicated lock so it is safe to call while holding e.mu.
// #3780 B: the old forward iteration broke at n=3, returning the OLDEST
// 3 attempts of the window - the violation's "last ran ... which FAILED"
// attribution then described the 3rd-oldest attempt (e.g. an old failure
// while the newest run succeeded without artifact), sending repair down
// the wrong path. Walk backwards, then restore oldest-first order.
func (e *workflowEngine) recentAttempts(stepID string, n int) []StepAttempt {
	if e == nil || stepID == "" || n <= 0 {
		return nil
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	var rev []StepAttempt
	for i := len(e.trace) - 1; i >= 0 && len(rev) < n; i-- {
		if e.trace[i].StepID == stepID {
			rev = append(rev, e.trace[i])
		}
	}
	if len(rev) == 0 {
		return nil
	}
	out := make([]StepAttempt, len(rev))
	for i, a := range rev {
		out[len(rev)-1-i] = a
	}
	return out
}

// stepAttemptedEver reports whether any command matching stepID ran this
// session, per the non-evicting lifetime count (#3836 B). Safe to call
// while holding e.mu: takes traceMu only (same discipline as
// recentAttempts).
func (e *workflowEngine) stepAttemptedEver(stepID string) bool {
	if e == nil || stepID == "" {
		return false
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	return e.attemptCounts[stepID] > 0
}

// wfAttemptAttribution renders the structural WHERE-it-broke diagnosis for
// a violation's counterexample step: never-attempted vs failed vs
// completed-without-artifact, each with its local repair.
func wfAttemptAttribution(v *WorkflowViolation) string {
	if v == nil || v.Missing == "" {
		return ""
	}
	if v.UnknownRequires {
		// #3822: the prerequisite step does not exist in the merged
		// spec. isComplete/groundIfFresh can never succeed - block mode
		// would reject the guarded commands forever while the generic
		// renderer claimed "the prerequisite was never executed".
		return fmt.Sprintf(" Attribution: step %q does not exist in the workflow spec (requires typo?) - the requirement can never be satisfied. Local repair: fix the requires entry in workflow-spec.json to reference a declared step ID.", v.Missing)
	}
	last := v.LastAttempt
	switch {
	case last == nil && v.AttemptedEver:
		// #3836 B: the per-step lifetime count says the step DID run this
		// session; its trace entry simply fell out of the shared 30-slot
		// sliding window (high-frequency sibling steps evicted it). The old
		// wording asserted "never executed" - factually wrong and pointing
		// repair at re-running instead of at the real issue (the artifact
		// never grounded).
		return fmt.Sprintf(" Attribution: step %q was attempted earlier this run, but its attempt record fell out of the recent trace window. Local repair: re-run the step %q command to re-ground its artifact (%q).", v.Missing, v.Missing, v.WantGlob)
	case last == nil:
		return fmt.Sprintf(" Attribution: no command matching step %q was attempted this run - the prerequisite was never executed. Local repair: run the step %q command first.", v.Missing, v.Missing)
	case last.IsError:
		return fmt.Sprintf(" Attribution: step %q last ran %q which FAILED (%s) at %s. Local repair: fix that command and re-run it (expected artifact %q), or fix the workflow spec.", v.Missing, last.Command, last.ErrSnippet, last.At.Format("15:04:05"), v.WantGlob)
	case !last.ProducedArtifact:
		return fmt.Sprintf(" Attribution: step %q last ran %q successfully at %s but it produced no artifact matching %q. Local repair: adjust the command to emit the expected artifact, or fix the spec's artifact_glob.", v.Missing, last.Command, last.At.Format("15:04:05"), v.WantGlob)
	default:
		// Grounded attempt should have lifted the requirement; keep a
		// defensive pointer to the spec in case of a race with a fresh
		// check immediately after grounding.
		return fmt.Sprintf(" Attribution: step %q's command succeeded and its artifact matched, but the check ran before it was registered. Re-run the command.", v.Missing)
	}
}

// wfTraceRecord is the Agent-side entry: after a tool executed, feed the
// outcome to the workflow engine's attempt ledger (no-op when inert).
func (a *Agent) wfTraceRecord(tc provider.ToolCallDelta, res tool.Result) {
	if e := a.workflowEngineLazy(); e != nil {
		e.recordAttempt(tc.Name, tc.Arguments, res)
	}
}
