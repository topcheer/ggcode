// Package agentruntime: dynamic workflow orchestration (r436).
//
// Research basis: Anthropic "Introducing dynamic workflows in Claude Code"
// (2026-05-28, GA) — orchestration externalization. The agent writes a
// structured task graph (decompose → parallel-solve → adversarial verify →
// synthesize) and an independent runtime executes it; the parent agent's
// context receives only the final synthesized answer, not intermediate
// results. This structurally prevents the three single-loop failure modes
// turn-by-turn delegation cannot: agentic laziness (the graph enumerates
// every step; the runtime enforces completion), self-preferential bias
// (verifier sub-agents are independent contexts trying to REFUTE each
// finding), and goal drift (constraints live in the graph, not in a
// compactable conversation).
//
// Difference from prior art in this repo (deliberate boundaries):
//   - r416 dag-tool-execution schedules TOOLS within one turn; this
//     orchestrates SUB-AGENTS across a whole task.
//   - swarm/teammate is a persistent team with a human/agent-curated board;
//     this is a one-shot generated task graph that runs to completion.
//   - best_of_n (r377) races N candidates on ONE task; this runs N
//     DIFFERENT tasks with dependencies and per-step verification.
//
// Reuse: the spawn pipeline is exactly best_of_n's — CandidateSpawner
// (tool.SpawnAgentTool via Launch) + SnapshotSource (*subagent.Manager).
// Sub-agents cannot nest (subAgentBlockedTools), so workflow fan-out depth
// is hard-capped at one level.
package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	// workflowMaxSteps bounds graph size: 16 steps at one level of nesting
	// already dwarfs what one turn-by-turn delegation loop manages reliably.
	workflowMaxSteps = 16
	// workflowLayerCap bounds per-layer concurrency: the session-wide spawn
	// budget is 16 slots shared with the parent's own tool calls, so a
	// workflow layer takes at most half of it.
	workflowLayerCap = 8
	// workflowDefaultPoll mirrors bestOfNDefaultPoll.
	workflowDefaultPoll = 2 * time.Second
)

// WorkflowStep is one node of the generated task graph. The LLM emits these
// as JSON via the workflow_run tool; the runtime never interprets free-form
// scripts (no JS sandbox needed — Anthropic's semantics, a reviewable
// subset of its surface).
type WorkflowStep struct {
	ID         string   `json:"id"`
	Task       string   `json:"task"`
	DependsOn  []string `json:"dependsOn,omitempty"`
	Verifier   string   `json:"verifier,omitempty"` // adversarial refute prompt; "" = no verification
	Tools      []string `json:"tools,omitempty"`
	Model      string   `json:"model,omitempty"`
	workerID   string   // runtime: sub-agent ID
	verifierID string   // runtime: verifier sub-agent ID
}

// WorkflowSpec is the full generated graph.
type WorkflowSpec struct {
	Steps     []WorkflowStep `json:"steps"`
	Synthesis string         `json:"synthesis"` // instruction for the final synthesizer
}

// WorkflowReport is what the parent context receives — step one-liners and
// the synthesized answer, never intermediate artifacts.
type WorkflowReport struct {
	StepLines  []string
	Synthesis  string
	Err        string
	Partial    bool
	LiveStepID map[string]string // step ID -> still-running sub-agent ID (Partial only)
}

// Validate checks spec sanity: non-empty, unique IDs, resolvable deps,
// acyclic, size caps. Returns the Kahn topological layers on success.
func (s *WorkflowSpec) Validate() ([][]int, error) {
	if s == nil || len(s.Steps) == 0 {
		return nil, fmt.Errorf("workflow: steps is required (1..%d)", workflowMaxSteps)
	}
	if len(s.Steps) > workflowMaxSteps {
		return nil, fmt.Errorf("workflow: %d steps exceeds cap %d", len(s.Steps), workflowMaxSteps)
	}
	if strings.TrimSpace(s.Synthesis) == "" {
		return nil, fmt.Errorf("workflow: synthesis instruction is required")
	}
	idx := make(map[string]int, len(s.Steps))
	for i, st := range s.Steps {
		if strings.TrimSpace(st.ID) == "" {
			return nil, fmt.Errorf("workflow: step %d has empty id", i)
		}
		if strings.TrimSpace(st.Task) == "" {
			return nil, fmt.Errorf("workflow: step %q has empty task", st.ID)
		}
		if _, dup := idx[st.ID]; dup {
			return nil, fmt.Errorf("workflow: duplicate step id %q", st.ID)
		}
		idx[st.ID] = i
	}
	// Kahn layering over DependsOn edges; a cycle leaves unreachable nodes.
	indeg := make([]int, len(s.Steps))
	dependents := make([][]int, len(s.Steps))
	for i, st := range s.Steps {
		seen := make(map[string]bool, len(st.DependsOn))
		for _, d := range st.DependsOn {
			j, ok := idx[d]
			if !ok {
				return nil, fmt.Errorf("workflow: step %q depends on unknown step %q", st.ID, d)
			}
			if j == i {
				return nil, fmt.Errorf("workflow: step %q depends on itself", st.ID)
			}
			if seen[d] {
				return nil, fmt.Errorf("workflow: step %q duplicates dependency %q", st.ID, d)
			}
			seen[d] = true
			indeg[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	var layers [][]int
	queue := make([]int, 0, len(s.Steps))
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	visited := 0
	for len(queue) > 0 {
		sort.Ints(queue) // deterministic layer order
		var next []int
		for _, i := range queue {
			visited++
			for _, dep := range dependents[i] {
				indeg[dep]--
				if indeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		layers = append(layers, queue)
		queue = next
	}
	if visited != len(s.Steps) {
		return nil, fmt.Errorf("workflow: dependency cycle detected (%d of %d steps reachable)", visited, len(s.Steps))
	}
	for _, l := range layers {
		if len(l) > workflowLayerCap {
			return nil, fmt.Errorf("workflow: layer has %d steps, cap is %d (add dependencies to split it)", len(l), workflowLayerCap)
		}
	}
	return layers, nil
}

// workflowTaskSuffix makes each worker self-sufficient and forces an
// explicit evidence line, mirroring best_of_n's candidateTaskSuffix.
const workflowTaskSuffix = "\n\nYou are one step of a parallel workflow graph. Work self-sufficiently: no coordination with other steps, no assumptions about them. Downstream steps only see your final message, so state your concrete result and evidence explicitly (files touched, command outcomes, findings list)."

// RunWorkflow executes the graph layer by layer. Semantics per step:
//  1. Launch a worker sub-agent (independent context).
//  2. On completion with a Verifier prompt, launch an adversarial verifier
//     sub-agent that must try to REFUTE the worker's result. A verifier
//     verdict starting with REJECTED strikes the step's output from the
//     synthesis inputs (it stays in the report as refuted).
//  3. After all layers, one synthesizer sub-agent folds surviving outputs
//     into the single answer the parent context receives.
//
// Context cancellation yields Partial with live step IDs; running workers
// are NOT cancelled (the parent may wait_agent them).
func RunWorkflow(ctx context.Context, spawner CandidateSpawner, snaps SnapshotSource, spec WorkflowSpec, poll time.Duration) WorkflowReport {
	rep := WorkflowReport{LiveStepID: map[string]string{}}
	if poll <= 0 {
		poll = workflowDefaultPoll
	}
	layers, err := spec.Validate()
	if err != nil {
		rep.Err = err.Error()
		return rep
	}

	results := make([]string, len(spec.Steps))     // worker final messages
	refuted := make([]bool, len(spec.Steps))       // adversarial-verdict strike
	statuses := make([]string, len(spec.Steps))    // terminal status per step
	blocking := make([]string, 0, len(spec.Steps)) // refuted/broken step IDs

	for _, layer := range layers {
		if ctx.Err() != nil {
			rep.Partial = true
			break
		}
		type launched struct {
			i  int
			id string
		}
		var ls []launched
		for _, i := range layer {
			st := &spec.Steps[i]
			task := st.Task + workflowTaskSuffix
			// A refuted dependency poisons downstream consumers: surface the
			// break instead of silently building on struck output.
			poisoned := false
			for _, d := range st.DependsOn {
				for _, b := range blocking {
					if d == b {
						poisoned = true
					}
				}
			}
			if poisoned {
				statuses[i] = "blocked"
				refuted[i] = true
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] BLOCKED: a dependency was refuted", st.ID))
				blocking = append(blocking, st.ID)
				continue
			}
			id, _, err := spawner.Launch(ctx, tool.LaunchOptions{
				Name:        "wf-" + st.ID,
				Task:        task,
				DisplayTask: "workflow step " + st.ID,
				Tools:       st.Tools,
				Model:       st.Model,
			})
			if err != nil {
				statuses[i] = "launch-failed"
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] LAUNCH FAILED: %v", st.ID, err))
				blocking = append(blocking, st.ID)
				continue
			}
			st.workerID = id
			ls = append(ls, launched{i, id})
		}

		// Poll the layer to terminal state (best_of_n pattern).
		terminal := make(map[int]bool, len(ls))
		for {
			allDone := true
			for _, l := range ls {
				if terminal[l.i] {
					continue
				}
				s, ok := snaps.Snapshot(l.id)
				if !ok {
					terminal[l.i] = true
					statuses[l.i] = "failed"
					results[l.i] = "sub-agent not found"
					continue
				}
				switch s.Status {
				case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
					terminal[l.i] = true
					statuses[l.i] = string(s.Status)
					results[l.i] = s.Result
				default:
					allDone = false
				}
			}
			if allDone {
				break
			}
			select {
			case <-ctx.Done():
				rep.Partial = true
				for _, l := range ls {
					if !terminal[l.i] {
						rep.LiveStepID[spec.Steps[l.i].ID] = l.id
					}
				}
				return rep
			case <-time.After(poll):
			}
		}

		// Adversarial verification for completed steps with a Verifier prompt.
		for _, l := range ls {
			i := l.i
			st := &spec.Steps[i]
			if statuses[i] != string(subagent.StatusCompleted) || strings.TrimSpace(st.Verifier) == "" {
				continue
			}
			verdict := runVerifier(ctx, spawner, snaps, st, results[i], poll)
			switch {
			case verdict == "":
				// verifier itself failed to run: keep the result, note it.
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] completed (verifier unavailable — unverified)", st.ID))
			case strings.HasPrefix(verdict, "REJECTED"):
				refuted[i] = true
				blocking = append(blocking, st.ID)
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] REFUTED by adversarial verifier: %s", st.ID, firstLine(verdict)))
			default:
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] completed (verifier upheld)", st.ID))
			}
		}
	}

	// Synthesis: fold SURVIVING step outputs into one answer via a final
	// sub-agent; the parent context gets this, not the raw outputs.
	var synthInput strings.Builder
	fmt.Fprintf(&synthInput, "Synthesis instruction: %s\n\nStep outcomes (REFUTED steps were struck by adversarial verification; BLOCKED steps lost a refuted dependency):\n", spec.Synthesis)
	anySurviving := false
	for i, st := range spec.Steps {
		switch {
		case refuted[i] && statuses[i] == "blocked":
			fmt.Fprintf(&synthInput, "- %s: BLOCKED (dependency refuted)\n", st.ID)
		case refuted[i]:
			fmt.Fprintf(&synthInput, "- %s: REFUTED — %s\n", st.ID, firstLine(results[i]))
		case statuses[i] == string(subagent.StatusCompleted):
			anySurviving = true
			fmt.Fprintf(&synthInput, "- %s (verified output):\n%s\n\n", st.ID, results[i])
		default:
			fmt.Fprintf(&synthInput, "- %s: %s\n", st.ID, statuses[i])
		}
	}
	if anySurviving && !rep.Partial {
		id, _, err := spawner.Launch(ctx, tool.LaunchOptions{
			Name:        "wf-synthesis",
			Task:        synthInput.String(),
			DisplayTask: "workflow synthesis",
		})
		if err == nil {
			if out, ok := waitOne(ctx, snaps, id, poll); ok && out.Status == subagent.StatusCompleted {
				rep.Synthesis = out.Result
			} else if ctx.Err() != nil {
				rep.Partial = true
				rep.LiveStepID["synthesis"] = id
			} else {
				rep.Synthesis = fmt.Sprintf("(synthesizer %s: %s)", out.Status, firstLine(out.Error))
			}
		}
	}
	if rep.Synthesis == "" && rep.Err == "" && !rep.Partial {
		if anySurviving {
			rep.Synthesis = "(synthesis unavailable)"
		} else {
			rep.Synthesis = "(no surviving steps to synthesize)"
		}
	}
	return rep
}

// verifierTaskPrefix frames the adversarial contract: the verifier's job is
// to try to BREAK the worker's claim, not to rubber-stamp it.
const verifierTaskPrefix = "You are an ADVERSARIAL VERIFIER for a workflow step. Your job is to try to REFUTE the worker's result: re-check its evidence, re-run narrow checks, look for missed edge cases and overclaims. If the result does not hold up, start your final message with 'REJECTED:' followed by the disproof. If it survives your attack, start your final message with 'UPHELD:' followed by the strongest residual caveat.\n\nVerification focus:\n"

// runVerifier launches one verifier and returns its first final-message
// token line ("REJECTED:..." / "UPHELD:..."), or "" when it could not run.
func runVerifier(ctx context.Context, spawner CandidateSpawner, snaps SnapshotSource, st *WorkflowStep, workerResult string, poll time.Duration) string {
	id, _, err := spawner.Launch(ctx, tool.LaunchOptions{
		Name:        "wf-verify-" + st.ID,
		Task:        verifierTaskPrefix + st.Verifier + "\n\nWorker result to attack:\n" + workerResult,
		DisplayTask: "workflow verify " + st.ID,
	})
	if err != nil {
		return ""
	}
	st.verifierID = id
	s, ok := waitOne(ctx, snaps, id, poll)
	if !ok || s.Status != subagent.StatusCompleted {
		return ""
	}
	return strings.TrimSpace(s.Result)
}

// waitOne polls a single sub-agent to terminal state.
func waitOne(ctx context.Context, snaps SnapshotSource, id string, poll time.Duration) (subagent.Snapshot, bool) {
	for {
		s, ok := snaps.Snapshot(id)
		if !ok {
			return subagent.Snapshot{ID: id, Status: subagent.StatusFailed, Error: "sub-agent not found"}, true
		}
		switch s.Status {
		case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
			return s, true
		}
		select {
		case <-ctx.Done():
			return s, false
		case <-time.After(poll):
		}
	}
}

// firstLine clips a message to its first line for one-liner report entries.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.TrimSpace(s)
}

// WorkflowRunnerFor wires the cycle-breaker: internal/tool cannot import
// internal/agentruntime, so the registration sites pass SpawnAgentTool and
// the Manager in. Mirrors BestOfNRunnerFor.
func WorkflowRunnerFor(sp CandidateSpawner, snaps SnapshotSource) func(context.Context, tool.WorkflowRequest) string {
	return func(ctx context.Context, req tool.WorkflowRequest) string {
		var spec WorkflowSpec
		if err := json.Unmarshal(req.Workflow, &spec); err != nil {
			return "workflow: invalid task graph JSON: " + err.Error()
		}
		rep := RunWorkflow(ctx, sp, snaps, spec, 0)
		return rep.Format()
	}
}

// Format renders the report the parent context receives: workflow header,
// per-step one-liners, and the synthesized answer.
func (r WorkflowReport) Format() string {
	var b strings.Builder
	if r.Err != "" {
		return "workflow: " + r.Err
	}
	fmt.Fprintf(&b, "workflow: %d steps ran under an external task graph (parent context holds only this report).\n", len(r.StepLines))
	for _, l := range r.StepLines {
		b.WriteString(l + "\n")
	}
	if r.Partial {
		b.WriteString("\nPARTIAL: caller context expired. Still-running step sub-agents (wait_agent these IDs):\n")
		ids := make([]string, 0, len(r.LiveStepID))
		for _, id := range r.LiveStepID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			b.WriteString("  " + id + "\n")
		}
	}
	fmt.Fprintf(&b, "\nSynthesized answer:\n%s\n", r.Synthesis)
	return b.String()
}
