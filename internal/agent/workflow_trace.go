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
	if e == nil || toolName != "run_command" {
		return
	}
	e.loadWorkflowSpec()
	e.mu.RLock()
	command, _ := parseRunCommandArgs(args)
	stepID, hasGlob := "", false
	if command != "" {
		for _, id := range e.order {
			st := e.steps[id]
			if commandMatches(st.OnCommands, command) {
				stepID, hasGlob = id, st.ArtifactGlob != ""
				break
			}
		}
	}
	// Artifact grounding reuses the #3414 safety-net probe (walk is done
	// under RLock, same precedent as checkPreconditions -> groundIfFresh).
	grounded := false
	if stepID != "" && hasGlob && !res.IsError {
		grounded = e.groundIfFresh(stepID)
	}
	e.mu.RUnlock()
	if stepID == "" {
		return // command belongs to no guarded step: nothing to attribute
	}
	snip := res.Content
	if len(snip) > wfErrSnippetMax {
		snip = snip[:wfErrSnippetMax]
	}
	a := StepAttempt{
		StepID:           stepID,
		Command:          command,
		IsError:          res.IsError,
		ErrSnippet:       snip,
		ProducedArtifact: grounded,
		At:               time.Now(),
	}
	e.traceMu.Lock()
	e.trace = append(e.trace, a)
	if len(e.trace) > wfTraceWindow {
		e.trace = e.trace[len(e.trace)-wfTraceWindow:]
	}
	e.traceMu.Unlock()
}

// recentAttempts returns up to n attempts recorded for stepID (oldest
// first). Uses a dedicated lock so it is safe to call while holding e.mu.
func (e *workflowEngine) recentAttempts(stepID string, n int) []StepAttempt {
	if e == nil || stepID == "" || n <= 0 {
		return nil
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	var out []StepAttempt
	for _, a := range e.trace {
		if a.StepID == stepID {
			out = append(out, a)
			if len(out) >= n {
				break
			}
		}
	}
	return out
}

// wfAttemptAttribution renders the structural WHERE-it-broke diagnosis for
// a violation's counterexample step: never-attempted vs failed vs
// completed-without-artifact, each with its local repair.
func wfAttemptAttribution(v *WorkflowViolation) string {
	if v == nil || v.Missing == "" {
		return ""
	}
	last := v.LastAttempt
	switch {
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
