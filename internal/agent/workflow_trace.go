package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const wfTraceWindow = 30

func isCommandExecTool(toolName string) bool {
	return toolName == "run_command" || toolName == "start_command"
}

const wfTraceMaxPerViolation = 3

const wfErrSnippetMax = 160

type StepAttempt struct {
	StepID           string    `json:"step_id"`
	Command          string    `json:"command"`
	IsError          bool      `json:"is_error"`
	ErrSnippet       string    `json:"err_snippet,omitempty"`
	ProducedArtifact bool      `json:"produced_artifact"`
	At               time.Time `json:"at"`
}

func (e *workflowEngine) recordAttempt(toolName string, args json.RawMessage, res tool.Result) {
	if e == nil || !isCommandExecTool(toolName) {
		return
	}
	e.loadWorkflowSpec()
	e.mu.RLock()
	command, _ := parseRunCommandArgs(args)
	var hits []string
	grounded := map[string]bool{}
	if command != "" {
		for _, id := range e.order {
			st := e.steps[id]
			if !commandMatches(st.OnCommands, command) {
				continue
			}
			hits = append(hits, id)
			if res.IsError {
				continue
			}

			if st.ArtifactGlob != "" {
				grounded[id] = e.groundIfFresh(id)
				continue
			}

			if _, done := e.completed.Load(id); !done {
				e.completed.Store(id, struct{}{})
				debug.Log("agent", "[workflow-spec] step %s complete (guarded command succeeded: %q)", id, command)
			}
		}
	}
	e.mu.RUnlock()
	if len(hits) == 0 {
		return
	}

	snip := res.Content
	if len(snip) > wfErrSnippetMax {
		snip = snip[len(snip)-wfErrSnippetMax:]
	}
	snip = strings.ToValidUTF8(snip, "?")
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

func (e *workflowEngine) stepAttemptedEver(stepID string) bool {
	if e == nil || stepID == "" {
		return false
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	return e.attemptCounts[stepID] > 0
}

func wfAttemptAttribution(v *WorkflowViolation) string {
	if v == nil || v.Missing == "" {
		return ""
	}
	if v.UnknownRequires {

		return fmt.Sprintf(" Attribution: step %q does not exist in the workflow spec (requires typo?) - the requirement can never be satisfied. Local repair: fix the requires entry in workflow-spec.json to reference a declared step ID.", v.Missing)
	}
	last := v.LastAttempt
	switch {
	case last == nil && v.AttemptedEver:

		return fmt.Sprintf(" Attribution: step %q was attempted earlier this run, but its attempt record fell out of the recent trace window. Local repair: re-run the step %q command to re-ground its artifact (%q).", v.Missing, v.Missing, v.WantGlob)
	case last == nil:
		return fmt.Sprintf(" Attribution: no command matching step %q was attempted this run - the prerequisite was never executed. Local repair: run the step %q command first.", v.Missing, v.Missing)
	case last.IsError:
		return fmt.Sprintf(" Attribution: step %q last ran %q which FAILED (%s) at %s. Local repair: fix that command and re-run it (expected artifact %q), or fix the workflow spec.", v.Missing, last.Command, last.ErrSnippet, last.At.Format("15:04:05"), v.WantGlob)
	case !last.ProducedArtifact:
		return fmt.Sprintf(" Attribution: step %q last ran %q successfully at %s but it produced no artifact matching %q. Local repair: adjust the command to emit the expected artifact, or fix the spec's artifact_glob.", v.Missing, last.Command, last.At.Format("15:04:05"), v.WantGlob)
	default:

		return fmt.Sprintf(" Attribution: step %q's command succeeded and its artifact matched, but the check ran before it was registered. Re-run the command.", v.Missing)
	}
}

func (a *Agent) wfTraceRecord(tc provider.ToolCallDelta, res tool.Result) {
	if e := a.workflowEngineLazy(); e != nil {
		e.recordAttempt(tc.Name, tc.Arguments, res)
	}
}
