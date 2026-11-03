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
	workflowMaxSteps    = 16
	workflowMaxRetries  = 2
	workflowLayerCap    = 8
	workflowDefaultPoll = 2 * time.Second
)

type WorkflowStep struct {
	ID         string   `json:"id"`
	Task       string   `json:"task"`
	DependsOn  []string `json:"dependsOn,omitempty"`
	Verifier   string   `json:"verifier,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	Model      string   `json:"model,omitempty"`
	Retries    int      `json:"retries,omitempty"`
	workerID   string
	verifierID string
}

type WorkflowSpec struct {
	Steps     []WorkflowStep `json:"steps"`
	Synthesis string         `json:"synthesis"`
}

type WorkflowReport struct {
	StepLines  []string
	Synthesis  string
	Err        string
	Partial    bool
	LiveStepID map[string]string
}

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
		if st.Retries < 0 || st.Retries > workflowMaxRetries {
			return nil, fmt.Errorf("workflow: step %q retries %d out of range 0..%d", st.ID, st.Retries, workflowMaxRetries)
		}
	}

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
		sort.Ints(queue)
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

	return layers, nil
}

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

const workflowTaskSuffix = "\n\nYou are one step of a parallel workflow graph. Work self-sufficiently: no coordination with other steps, no assumptions about them. Downstream steps only see your final message, so state your concrete result and evidence explicitly (files touched, command outcomes, findings list)."

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

	results := make([]string, len(spec.Steps))
	refuted := make([]bool, len(spec.Steps))
	statuses := make([]string, len(spec.Steps))
	blocking := make([]string, 0, len(spec.Steps))

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
			return da > db
		}
		return a < b
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

	attempts := make([]int, len(spec.Steps))
	retryStep := func(i int, reason string) bool {
		st := &spec.Steps[i]
		if attempts[i] >= st.Retries {
			return false
		}
		attempts[i]++
		doneStep[i] = true
		rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] %s — retrying (attempt %d/%d)", st.ID, reason, attempts[i], st.Retries))
		ready = append(ready, i)
		sort.Slice(ready, func(x, y int) bool { return readyLess(ready[x], ready[y]) })
		return true
	}

	finalize := func(i int, status, result string) {
		switch status {
		case string(subagent.StatusFailed), string(subagent.StatusCancelled):
			if retryStep(i, status+" (transient)") {
				return
			}
		}
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
				if retryStep(i, "refuted by verifier") {
					return
				}
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
			for _, a := range act {
				if !doneStep[a.i] {
					rep.LiveStepID[spec.Steps[a.i].ID] = a.id
				}
			}
			return rep
		}

		for len(ready) > 0 && len(act) < workflowLayerCap {
			i := ready[0]
			ready = ready[1:]
			st := &spec.Steps[i]

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
				if retryStep(i, "launch failed") {
					continue
				}
				statuses[i] = "launch-failed"
				rep.StepLines = append(rep.StepLines, fmt.Sprintf("[%s] LAUNCH FAILED: %v", st.ID, err))
				blocking = append(blocking, st.ID)
				release(i)
				continue
			}
			st.workerID = id
			doneStep[i] = false
			act = append(act, inFlight{i, id})
		}

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
			}
		}

		kept := act[:0]
		for _, a := range act {
			if !doneStep[a.i] {
				kept = append(kept, a)
			}
		}
		act = kept
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

const verifierTaskPrefix = "You are an ADVERSARIAL VERIFIER for a workflow step. Your job is to try to REFUTE the worker's result: re-check its evidence, re-run narrow checks, look for missed edge cases and overclaims. If the result does not hold up, start your final message with 'REJECTED:' followed by the disproof. If it survives your attack, start your final message with 'UPHELD:' followed by the strongest residual caveat.\n\nVerification focus:\n"

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

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.TrimSpace(s)
}

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
