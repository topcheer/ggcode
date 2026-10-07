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
	// Layer width is no longer a hard error (research sa-123): the runtime
	// schedules a ready set bounded by workflowLayerCap, so wide graphs queue
	// instead of being rejected.
	return layers, nil
}

// downstreamDepth returns, per step ID, the length of the longest chain from
// that step to a sink (itself counted). Computed by reverse topological DP
// over the Kahn layers from Validate; used to prioritize the longest
// downstream chain (critical path) when the in-flight window is contended.
func (s *WorkflowSpec) downstreamDepth(layers [][]int) map[string]int {
	dependents := make(map[string][]string, len(s.Steps))
	for _, st := range s.Steps {
		for _, d := range st.DependsOn {
			dependents[d] = append(dependents[d], st.ID)
		}
	}
	depth := make(map[string]int, len(s.Steps))
	for li := len(layers) - 1; li >= 0; li-- {
		for _, i := range layers[li] {
			id := s.Steps[i].ID
			d := 1
			for _, child := range dependents[id] {
				if cd := depth[child] + 1; cd > d {
					d = cd
				}
			}
			depth[id] = d
		}
	}
	return depth
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

	// Ready-set scheduling with critical-path priority (research sa-123,
	// latency-aware orchestration): a step launches the moment its last
	// dependency reaches a terminal state instead of waiting for its whole
	// Kahn layer to drain, and when the in-flight window (workflowLayerCap)
	// is contended the longest downstream chain goes first. Single-layer
	// graphs and uncontended windows behave exactly like the old barrier.
	depth := spec.downstreamDepth(layers)
	idx := make(map[string]int, len(spec.Steps))
	depsLeft := make([]int, len(spec.Steps))
	dependents := make(map[int][]int, len(spec.Steps))
	for i, st := range spec.Steps {
		idx[st.ID] = i
		depsLeft[i] = len(st.DependsOn)
		for _, d := range st.DependsOn {
			dependents[idx[d]] = append(dependents[idx[d]], i)
		}
	}
	readyLess := func(a, b int) bool {
		da, db := depth[spec.Steps[a].ID], depth[spec.Steps[b].ID]
		if da != db {
			return da > db // deepest downstream chain first
		}
		return a < b // stable tie-break by spec order
	}
	var ready []int
	for i := range spec.Steps {
		if depsLeft[i] == 0 {
			ready = append(ready, i)
		}
	}
	sort.Slice(ready, func(x, y int) bool { return readyLess(ready[x], ready[y]) })

	doneStep := make([]bool, len(spec.Steps))
	release := func(i int) {
		for _, j := range dependents[i] {
			depsLeft[j]--
			if depsLeft[j] == 0 {
				ready = append(ready, j)
				sort.Slice(ready, func(x, y int) bool { return readyLess(ready[x], ready[y]) })
			}
		}
	}
	// finalize records a terminal step, runs its adversarial verifier when
	// configured, then releases dependents into the ready set.
	finalize := func(i int, status, result string) {
		statuses[i] = status
		results[i] = result
		doneStep[i] = true
		st := &spec.Steps[i]
		if status == string(subagent.StatusCompleted) && strings.TrimSpace(st.Verifier) != "" {
			verdict := runVerifier(ctx, spawner, snaps, st, result, poll)
			switch {
			case verdict == "":
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] completed (verifier unavailable — unverified)", st.ID))
			case strings.HasPrefix(verdict, "REJECTED"):
				refuted[i] = true
				blocking = append(blocking, st.ID)
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] REFUTED by adversarial verifier: %s", st.ID, firstLine(verdict)))
			default:
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] completed (verifier upheld)", st.ID))
			}
		}
		release(i)
	}

	type inFlight struct {
		i  int
		id string
	}
	var act []inFlight
	for len(ready) > 0 || len(act) > 0 {
		if ctx.Err() != nil {
			rep.Partial = true
			break
		}
		// Fill the in-flight window. Blocked (poisoned) and launch-failed
		// steps free their slot immediately and release their dependents.
		for len(ready) > 0 && len(act) < workflowLayerCap {
			i := ready[0]
			ready = ready[1:]
			st := &spec.Steps[i]
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
				release(i)
				continue
			}
			id, _, err := spawner.Launch(ctx, tool.LaunchOptions{
				Name:        "wf-" + st.ID,
				Task:        st.Task + workflowTaskSuffix,
				DisplayTask: "workflow step " + st.ID,
				Tools:       st.Tools,
				Model:       st.Model,
			})
			if err != nil {
				statuses[i] = "launch-failed"
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] LAUNCH FAILED: %v", st.ID, err))
				blocking = append(blocking, st.ID)
				release(i)
				continue
			}
			st.workerID = id
			act = append(act, inFlight{i, id})
		}

		// Poll in-flight workers to terminal state (best_of_n pattern).
		allDone := true
		for _, a := range act {
			if doneStep[a.i] {
				continue
			}
			s, ok := snaps.Snapshot(a.id)
			if !ok {
				finalize(a.i, "failed", "sub-agent not found")
				continue
			}
			switch s.Status {
			case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
				finalize(a.i, string(s.Status), s.Result)
			default:
				allDone = false
			}
		}
		if allDone {
			act = act[:0] // compact finished entries
		}
		if len(act) == 0 && len(ready) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			rep.Partial = true
			for _, a := range act {
				if !doneStep[a.i] {
					rep.LiveStepID[spec.Steps[a.i].ID] = a.id
				}
			}
			return rep
		case <-time.After(poll):
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
