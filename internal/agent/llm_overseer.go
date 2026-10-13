package agent

// llm_overseer.go -- SICA semantic-level overseer (r489).
//
// Research basis: SICA (Robeyns et al. 2025, arXiv:2504.15228) runs an
// independent, cheap LLM in a concurrent thread that watches the agent's
// execution trace and intervenes on pathological behavior. The deterministic
// overseer (overseer.go) covers the mechanical five modes (spam, stall,
// stuck-on-file, error escalation, drift); its own header comment notes it
// "replaces the LLM with deterministic heuristics". This file restores the
// residual semantic facet the heuristics cannot see: WRONG APPROACH (solving
// the right problem with a method that cannot converge) and WRONG PROBLEM
// (working hard on something that is not the user's actual request).
//
// Dormancy: fully inert unless aux_model is configured (SetAuxModel wired
// auxResolved) - the same opt-in surface and zero-new-config-keys contract as
// model_routing.go / model_cascade.go.
//
// Design (per r489 backlog spec):
//   - Only for long runs: >= llmOverseerMinIters iterations.
//   - Every llmOverseerEvery iterations, take the last K tool-call
//     trajectory entries, make ONE cheap aux-model call asking for a JSON
//     verdict {verdict, evidence, suggestion}.
//   - verdict on_track -> return "" (do not disturb the loop).
//   - Hard budget: max llmOverseerMaxCalls calls per run; each call capped at
//     llmOverseerTimeout. Any failure (build, network, timeout, malformed
//     JSON) degrades silently to "" - the deterministic overseer still runs.
//   - Synchronous contract: called from the same agent-goroutine tool-result
//     path as overseerCheck; no goroutines, no locks beyond the trajectory
//     snapshot copy.
//   - Usage is accounted under the aux provider via the same emitUsage
//     channel the strategist uses.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	llmOverseerMinIters = 40 // only long runs qualify (heuristics handle the rest)
	llmOverseerEvery    = 24 // analysis cadence, in iterations
	llmOverseerMaxCalls = 2  // hard per-run budget
	llmOverseerWindow   = 24 // K most recent trajectory entries fed to the call
	llmOverseerTimeout  = 8 * time.Second
)

// llmOverseerVerdict values.
const (
	verdictOnTrack       = "on_track"
	verdictWrongApproach = "wrong_approach"
	verdictWrongProblem  = "wrong_problem"
)

// llmOverseerState is carried on Agent; zero value is fully dormant.
type llmOverseerState struct {
	callsDone int // calls consumed against the per-run budget
	lastIter  int // iteration of the last analysis (cadence gate)
}

// llmOverseerVerdictJSON is the contract the aux model is asked to emit.
type llmOverseerVerdictJSON struct {
	Verdict    string `json:"verdict"`
	Evidence   string `json:"evidence"`
	Suggestion string `json:"suggestion"`
}

// snapshotTrajectory returns (a copy of) the K most recent trajectory
// entries. Safe for use from the agent goroutine; takes the same overseer
// mutex the deterministic recorder uses.
func (a *Agent) snapshotTrajectory(k int) []trajectoryEntry {
	if a.overseer == nil {
		return nil
	}
	a.overseer.mu.Lock()
	defer a.overseer.mu.Unlock()
	n := len(a.overseer.trajectory)
	if n == 0 {
		return nil
	}
	if k > n {
		k = n
	}
	out := make([]trajectoryEntry, k)
	copy(out, a.overseer.trajectory[n-k:])
	return out
}

// llmOverseerCheck runs the semantic-level analysis when armed and due.
// Returns a guidance hint for appendGuidance, or "" when dormant, not yet
// due, budget-exhausted, or on any failure (silent degradation by design).
func (a *Agent) llmOverseerCheck(iteration int) string {
	if a.auxResolved == nil || a.overseer == nil {
		return "" // dormant: no aux model configured
	}
	if iteration < llmOverseerMinIters {
		return "" // short run: deterministic modes suffice
	}
	if iteration-a.llmOverseer.lastIter < llmOverseerEvery {
		return "" // cadence gate (shared with the budget gate below)
	}
	if a.llmOverseer.callsDone >= llmOverseerMaxCalls {
		return "" // per-run budget exhausted
	}
	traj := a.snapshotTrajectory(llmOverseerWindow)
	if len(traj) == 0 {
		return ""
	}

	a.llmOverseer.lastIter = iteration
	a.llmOverseer.callsDone++

	hint := a.llmOverseerCall(traj, iteration)
	if hint == "" {
		debug.Log("llm-overseer", "iteration %d: analysis inconclusive/silent-fail (%d/%d calls used)",
			iteration, a.llmOverseer.callsDone, llmOverseerMaxCalls)
	}
	return hint
}

// llmOverseerCall performs the single aux-model verdict call and maps a
// negative verdict to a guidance hint. All failures return "".
func (a *Agent) llmOverseerCall(traj []trajectoryEntry, iteration int) string {
	var b strings.Builder
	for i, e := range traj {
		mark := "ok"
		if e.isError {
			mark = "ERR"
		}
		line := e.toolName
		if e.fileHint != "" {
			line += " " + e.fileHint
		}
		fmt.Fprintf(&b, "%3d. [%s] %s\n", i+1, mark, line)
	}

	systemPrompt := `You are an execution overseer watching a coding agent's recent tool-call trajectory. ` +
		`Judge ONLY the trajectory shown, not the task statement. ` +
		`Reply with a single JSON object and nothing else: ` +
		`{"verdict":"on_track|wrong_approach|wrong_problem","evidence":"<one sentence citing trajectory indices>","suggestion":"<one concrete corrective instruction>"}. ` +
		`Use wrong_problem only when the work is clearly not the user's actual request. ` +
		`When in doubt, answer on_track.`

	var taskHint string
	if g := a.getAutopilotGoal(); g != "" {
		taskHint = g
	} else if msgs := a.Messages(); len(msgs) > 0 {
		for _, block := range msgs[0].Content {
			if block.Type == "text" {
				taskHint = block.Text
				break
			}
		}
	}
	if len(taskHint) > 600 {
		taskHint = taskHint[:600]
	}

	userPrompt := fmt.Sprintf(
		"## Task (truncated)\n%s\n\n## Last %d tool calls\n%s\nJudge the trajectory.",
		taskHint, len(traj), b.String())

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmOverseerTimeout)
	defer cancel()

	resp, err := a.auxProviderFor().Chat(ctx, messages, nil)
	if err != nil {
		debug.Log("llm-overseer", "aux chat failed at iteration %d: %v", iteration, err)
		return ""
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		a.emitUsageWithSource(resp.Usage, "llm-overseer")
	}

	var raw string
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			raw += block.Text
		}
	}
	v := llmOverseerParseVerdict(raw)
	if v == nil {
		debug.Log("llm-overseer", "malformed verdict at iteration %d (len=%d)", iteration, len(raw))
		return ""
	}

	switch v.Verdict {
	case verdictWrongApproach, verdictWrongProblem:
		hint := fmt.Sprintf("[llm-overseer] %s: %s — %s",
			v.Verdict, v.Evidence, v.Suggestion)
		debug.Log("llm-overseer", "verdict=%s at iteration %d", v.Verdict, iteration)
		return hint
	default:
		return "" // on_track (or unknown): do not disturb
	}
}

// llmOverseerParseVerdict extracts the JSON verdict object from a model
// reply that may wrap it in prose or code fences.
func llmOverseerParseVerdict(raw string) *llmOverseerVerdictJSON {
	if raw == "" {
		return nil
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil
	}
	var v llmOverseerVerdictJSON
	if err := json.Unmarshal([]byte(raw[start:end+1]), &v); err != nil {
		return nil
	}
	if v.Verdict == "" {
		return nil
	}
	return &v
}
