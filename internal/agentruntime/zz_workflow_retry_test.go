package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// wfRetryFake launches deterministic IDs per attempt so a RETRIED step (same
// wf-<id> name, second Launch) can return a different outcome than the
// first attempt - the shared wfFake cannot distinguish attempts.
type wfRetryFake struct {
	mu       sync.Mutex
	launches int
	// attempts maps launch name -> number of times launched.
	attempts map[string]int
	// outcomes maps name -> outcome per attempt index (0-based).
	outcomes map[string][]string // "ok:<result>" or "fail"
	// outcome maps agent id -> resolved terminal outcome.
	outcome map[string]string
	// tasks maps launch name -> task text (last launch wins).
	tasks map[string]string
	// running tracks the one-poll terminal flip like wfFake.
	running map[string]bool
}

func newWfRetryFake() *wfRetryFake {
	return &wfRetryFake{
		attempts: map[string]int{},
		outcomes: map[string][]string{},
		outcome:  map[string]string{},
		tasks:    map[string]string{},
		running:  map[string]bool{},
	}
}

// set scripts the outcomes for a step name, in launch order.
func (f *wfRetryFake) set(name string, outs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[name] = outs
}

func (f *wfRetryFake) Launch(ctx context.Context, opts tool.LaunchOptions) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches++
	n := f.attempts[opts.Name]
	f.attempts[opts.Name] = n + 1
	id := fmt.Sprintf("retry-fake-%s-%d", opts.Name, n)
	outs := f.outcomes[opts.Name]
	outcome := "ok:result of " + opts.Name
	if n < len(outs) {
		outcome = outs[n]
	} else if len(outs) > 0 {
		outcome = outs[len(outs)-1]
	}
	if !strings.HasPrefix(outcome, "fail") && !strings.HasPrefix(outcome, "REJECTED") && !strings.HasPrefix(outcome, "ok:") {
		outcome = "ok:" + outcome
	}
	if strings.Contains(opts.Name, "synthesis") {
		outcome = "ok:SYNTH-OK"
	}
	_ = outcome // results derived on demand below via stored map
	f.outcome[id] = outcome
	f.tasks[opts.Name] = opts.Task
	f.running[id] = true
	return id, "", nil
}

// outcome stores per-id terminal results.
// (declared as map on struct via below field)

func (f *wfRetryFake) Snapshot(id string) (subagent.Snapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running[id] {
		f.running[id] = false
		return subagent.Snapshot{ID: id, Status: subagent.StatusRunning}, true
	}
	out := f.outcome[id]
	if strings.HasPrefix(out, "fail") {
		return subagent.Snapshot{ID: id, Status: subagent.StatusFailed, Result: out}, true
	}
	if strings.HasPrefix(out, "REJECTED") {
		// verifier-style first line surfaced as a completed worker result
		return subagent.Snapshot{ID: id, Status: subagent.StatusCompleted, Result: out}, true
	}
	return subagent.Snapshot{ID: id, Status: subagent.StatusCompleted, Result: strings.TrimPrefix(out, "ok:")}, true
}

func (f *wfRetryFake) RunningCount() int { return 0 }

func (f *wfRetryFake) totalLaunches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launches
}

func (f *wfRetryFake) launchesFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[name]
}

func (f *wfRetryFake) taskFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks[name]
}

// TestWorkflowStepRetryRecoversFromTransientFailure: a step whose first
// worker fails and whose retry succeeds must complete and NOT poison its
// downstream dependent (LLMCompiler task-fetching semantics).
func TestWorkflowStepRetryRecoversFromTransientFailure(t *testing.T) {
	f := newWfRetryFake()
	// First attempt of the upstream step fails; the retry succeeds.
	f.set("wf-upstream", "fail:LLM 503", "ok:UPSTREAM-OK")

	spec := WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "upstream", Task: "produce evidence", Retries: 1},
			{ID: "downstream", Task: "consume evidence", DependsOn: []string{"upstream"}},
		},
		Synthesis: "fold the outputs",
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)

	if rep.Err != "" {
		t.Fatalf("unexpected workflow error: %s", rep.Err)
	}
	if rep.Partial {
		t.Fatalf("unexpected partial: %+v", rep.LiveStepID)
	}
	joined := strings.Join(rep.StepLines, "\n")
	if !strings.Contains(joined, "retrying (attempt 1/1)") {
		t.Fatalf("expected a retry StepLine, got:\n%s", joined)
	}
	if !strings.Contains(rep.Synthesis, "UPSTREAM-OK") && !strings.Contains(joined, "upstream") {
		t.Fatalf("upstream result missing from synthesis:\n%s\n---\n%s", joined, rep.Synthesis)
	}
	// The dependent must NOT be blocked.
	for _, ln := range rep.StepLines {
		if strings.Contains(ln, "downstream") && strings.Contains(ln, "BLOCKED") {
			t.Fatalf("downstream was poisoned despite retry:\n%s", joined)
		}
	}
	// upstream launched twice, downstream once, synthesis once.
	if got := f.totalLaunches(); got != 4 {
		t.Fatalf("expected 4 launches (upstream x2 + downstream + synthesis), got %d:\n%s", got, joined)
	}
}

// TestWorkflowRetryExhaustedFailsForReal: when the retry budget is
// exhausted the step strikes for real (status failed in synthesis) -
// the old one-strike behavior is preserved at the end of the budget.
func TestWorkflowRetryExhaustedFailsForReal(t *testing.T) {
	f := newWfRetryFake()
	f.set("wf-flaky", "fail:always fails") // every attempt fails

	spec := WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "flaky", Task: "doomed step", Retries: 1},
			{ID: "victim", Task: "runs without doomed output", DependsOn: []string{"flaky"}},
		},
		Synthesis: "fold the outputs",
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)

	joined := strings.Join(rep.StepLines, "\n")
	if !strings.Contains(joined, "retrying (attempt 1/1)") {
		t.Fatalf("expected one retry StepLine, got:\n%s", joined)
	}
	if strings.Count(joined, "retrying") != 1 {
		t.Fatalf("budget 1 must not retry twice:\n%s", joined)
	}
	// Budget exhausted: the step's terminal failure must surface in the
	// synthesizer's input, and the dependent still runs.
	if got := f.launchesFor("wf-flaky"); got != 2 {
		t.Fatalf("expected wf-flaky launched twice (initial + 1 retry), got %d", got)
	}
	if got := f.launchesFor("wf-victim"); got != 1 {
		t.Fatalf("expected wf-victim launched once, got %d", got)
	}
	if synth := f.taskFor("wf-synthesis"); !strings.Contains(synth, "flaky: failed") {
		t.Fatalf("synthesis input must surface flaky's terminal failure:\n%s", synth)
	}
}

// TestWorkflowRetriesValidation: out-of-range retries is rejected.
func TestWorkflowRetriesValidation(t *testing.T) {
	spec := WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "a", Task: "t", Retries: 3},
		},
		Synthesis: "s",
	}
	if _, err := spec.Validate(); err == nil || !strings.Contains(err.Error(), "retries") {
		t.Fatalf("expected retries range error, got %v", err)
	}
	spec.Steps[0].Retries = -1
	if _, err := spec.Validate(); err == nil || !strings.Contains(err.Error(), "retries") {
		t.Fatalf("expected negative retries error, got %v", err)
	}
}

// TestWorkflowRetryDefaultZeroBehaviorUnchanged: Retries=0 keeps the old
// one-strike semantics - a single failure strikes immediately (no retry
// StepLine, exactly one launch of the failing step).
func TestWorkflowRetryDefaultZeroBehaviorUnchanged(t *testing.T) {
	f := newWfRetryFake()
	f.set("wf-once", "fail:boom")

	spec := WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "once", Task: "fails once, no budget"},
			{ID: "child", Task: "child", DependsOn: []string{"once"}},
		},
		Synthesis: "s",
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)

	joined := strings.Join(rep.StepLines, "\n")
	if strings.Contains(joined, "retrying") {
		t.Fatalf("no retry expected with Retries=0:\n%s", joined)
	}
	if got := f.launchesFor("wf-once"); got != 1 {
		t.Fatalf("expected exactly 1 launch of the failing step, got %d", got)
	}
	if synth := f.taskFor("wf-synthesis"); !strings.Contains(synth, "once: failed") {
		t.Fatalf("synthesis input must surface the failure:\n%s", synth)
	}
}
