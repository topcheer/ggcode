package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// r436 probes: dynamic workflow runtime. Fake spawner/snaps mirror the
// best_of_n test harness - Launch records options, snapshots flip to
// completed after N polls so layering, verification, and synthesis are
// observable deterministically.

type wfFake struct {
	mu      sync.Mutex
	launch  int // launch count
	names   []string
	tasks   map[string]string
	seq     int
	results map[string]string // id -> final result text
	live    map[string]bool   // ids still running
}

func newWfFake() *wfFake {
	return &wfFake{tasks: map[string]string{}, results: map[string]string{}, live: map[string]bool{}}
}

func (f *wfFake) Launch(_ context.Context, opts tool.LaunchOptions) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launch++
	f.seq++
	id := "sa-fake-" + opts.Name
	f.names = append(f.names, opts.Name)
	f.tasks[id] = opts.Task
	switch {
	case strings.Contains(opts.Name, "synthesis"):
		f.results[id] = "SYNTH-OK"
	case strings.Contains(opts.Name, "wf-verify-refuted"):
		f.results[id] = "REJECTED: claim contradicts test evidence"
	case strings.Contains(opts.Name, "wf-verify-"):
		f.results[id] = "UPHELD: minor caveat"
	case strings.Contains(opts.Name, "refuted-worker"):
		f.results[id] = "worker claims X holds"
	default:
		f.results[id] = "result of " + opts.Name
	}
	// Workers start running; one poll later they complete (so the poll loop
	// observes a genuine running -> terminal transition).
	f.live[id] = true
	return id, "", nil
}

func (f *wfFake) Snapshot(id string) (subagent.Snapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res, done := f.results[id]; done && !f.live[id] {
		return subagent.Snapshot{ID: id, Status: subagent.StatusCompleted, Result: res}, true
	}
	// complete everything one poll in: simplest deterministic terminal flip.
	if f.live[id] {
		f.live[id] = false
	}
	return subagent.Snapshot{ID: id, Status: subagent.StatusRunning}, true
}

func (f *wfFake) RunningCount() int { return 0 }

func (f *wfFake) launchCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.launch }

// pollOnceSemantics: the fake completes agents on second observation, so a
// 1ms poll keeps tests fast.
const wfPoll = time.Millisecond

func TestWorkflowValidateLayersAndCycles(t *testing.T) {
	spec := &WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "a", Task: "ta"},
			{ID: "b", Task: "tb", DependsOn: []string{"a"}},
			{ID: "c", Task: "tc", DependsOn: []string{"a"}},
			{ID: "d", Task: "td", DependsOn: []string{"b", "c"}},
		},
		Synthesis: "fold",
	}
	layers, err := spec.Validate()
	if err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
	if len(layers) != 3 || len(layers[0]) != 1 || len(layers[1]) != 2 || len(layers[2]) != 1 {
		t.Fatalf("layers = %v, want 3 layers sized 1/2/1", layers)
	}

	bad := WorkflowSpec{Steps: []WorkflowStep{
		{ID: "x", Task: "t", DependsOn: []string{"y"}},
		{ID: "y", Task: "t", DependsOn: []string{"x"}},
	}, Synthesis: "s"}
	if _, err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle not detected: %v", err)
	}

	for _, tc := range []struct {
		name string
		mut  func(*WorkflowSpec)
		want string
	}{
		{"unknown dep", func(s *WorkflowSpec) { s.Steps[1].DependsOn = []string{"nope"} }, "unknown step"},
		{"self dep", func(s *WorkflowSpec) { s.Steps[0].DependsOn = []string{"a"} }, "itself"},
		{"dup id", func(s *WorkflowSpec) { s.Steps[1].ID = "a" }, "duplicate"},
		{"no synthesis", func(s *WorkflowSpec) { s.Synthesis = " " }, "synthesis"},
		{"empty steps", func(s *WorkflowSpec) { s.Steps = nil }, "required"},
	} {
		s := WorkflowSpec{
			Steps:     []WorkflowStep{{ID: "a", Task: "ta"}, {ID: "b", Task: "tb", DependsOn: []string{"a"}}},
			Synthesis: "fold",
		}
		tc.mut(&s)
		if _, err := s.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v want~%q", tc.name, err, tc.want)
		}
	}
}

func TestWorkflowRunLayeredExecutionAndAdversarialStrike(t *testing.T) {
	f := newWfFake()
	spec := WorkflowSpec{
		Steps: []WorkflowStep{
			{ID: "clean", Task: "t-clean", Verifier: "check the claim"},
			{ID: "refuted-worker", Task: "t-ref", Verifier: "attack it"},
			{ID: "downstream", Task: "t-down", DependsOn: []string{"refuted-worker"}},
		},
		Synthesis: "fold all",
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)
	if rep.Err != "" || rep.Partial {
		t.Fatalf("unexpected err/partial: %q %v", rep.Err, rep.Partial)
	}
	// 2 workers (downstream is poison-BLOCKED, never launched) + 2 verifiers
	// (the two steps with Verifier prompts) + 1 synthesis = 5 launches.
	if got := f.launchCount(); got != 5 {
		t.Fatalf("launches = %d, want 5 (2 workers + 2 verifiers + 1 synthesis; blocked step not launched)", got)
	}
	joined := strings.Join(rep.StepLines, "\n")
	if !strings.Contains(joined, "REFUTED by adversarial verifier") {
		t.Fatalf("refutation missing from report:\n%s", joined)
	}
	if !strings.Contains(joined, "BLOCKED") {
		t.Fatalf("poisoned downstream not marked BLOCKED:\n%s", joined)
	}
	if !strings.Contains(joined, "verifier upheld") {
		t.Fatalf("upheld step missing:\n%s", joined)
	}
	if rep.Synthesis != "SYNTH-OK" {
		t.Fatalf("synthesis = %q, want SYNTH-OK", rep.Synthesis)
	}
	// Parent-context contract: the formatted report carries step one-liners
	// and the synthesis, never raw worker output like "worker claims X holds".
	formatted := rep.Format()
	if strings.Contains(formatted, "worker claims X holds") {
		t.Fatalf("raw refuted output leaked into parent report:\n%s", formatted)
	}
	if !strings.Contains(formatted, "SYNTH-OK") {
		t.Fatalf("synthesis missing from formatted report")
	}
	// Verifier prompt must carry the adversarial frame and the worker result.
	f.mu.Lock()
	verifierTask := f.tasks[firstIDContaining(f, "wf-verify-refuted")]
	workerTask := f.tasks[firstIDContaining(f, "wf-refuted-worker")]
	f.mu.Unlock()
	if !strings.Contains(verifierTask, "REFUTE") || !strings.Contains(verifierTask, "worker claims X holds") {
		t.Fatalf("verifier task lacks adversarial frame or worker result:\n%s", verifierTask)
	}
	if !strings.Contains(workerTask, "one step of a parallel workflow") {
		t.Fatalf("worker task lacks self-sufficiency suffix:\n%s", workerTask)
	}
}

func firstIDContaining(f *wfFake, sub string) string {
	for id, task := range f.tasks {
		if strings.Contains(task, sub) || strings.Contains(id, sub) {
			return id
		}
	}
	return ""
}

func TestWorkflowRunnerForRejectsBadJSON(t *testing.T) {
	f := newWfFake()
	run := WorkflowRunnerFor(f, f)
	out := run(context.Background(), tool.WorkflowRequest{Workflow: json.RawMessage(`{"steps":`)})
	if !strings.Contains(out, "invalid task graph JSON") {
		t.Fatalf("bad JSON not surfaced: %q", out)
	}
	// Valid JSON but invalid graph: validation error surfaces through Format.
	out = run(context.Background(), tool.WorkflowRequest{Workflow: json.RawMessage(`{"steps":[{"id":"a","task":"t","dependsOn":["b"]},{"id":"b","task":"t","dependsOn":["a"]}],"synthesis":"s"}`)})
	if !strings.Contains(out, "cycle") {
		t.Fatalf("cycle not surfaced via runner: %q", out)
	}
}

// wfSlowFake defers completion of chosen worker IDs for extra polls, so
// ready-set scheduling decisions (launch vs. wait) are observable.
type wfSlowFake struct {
	wfFake
	slow map[string]int // worker id -> extra polls before terminal
}

func (f *wfSlowFake) Snapshot(id string) (subagent.Snapshot, bool) {
	if n := f.slow[id]; n > 0 {
		f.slow[id] = n - 1
		return subagent.Snapshot{ID: id, Status: subagent.StatusRunning}, true
	}
	return f.wfFake.Snapshot(id)
}

// TestWorkflowCriticalPathPriority (research sa-123): with equal readiness,
// the step on the longest downstream chain launches first.
func TestWorkflowCriticalPathPriority(t *testing.T) {
	f := newWfFake()
	spec := WorkflowSpec{
		Synthesis: "fold",
		Steps: []WorkflowStep{
			{ID: "chainA", Task: "t"},
			{ID: "chainB", Task: "t", DependsOn: []string{"chainA"}},
			{ID: "leafX", Task: "t"},
			{ID: "leafY", Task: "t"},
		},
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)
	if rep.Err != "" {
		t.Fatalf("unexpected error: %s", rep.Err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.names) < 1 || f.names[0] != "wf-chainA" {
		t.Fatalf("deepest-chain step should launch first, got %v", f.names)
	}
}

// TestWorkflowReadyFiresWithoutLayerBarrier (research sa-123): chainA->chainB
// plus a slow independent step; chainB must launch while the independent step
// is still running (old layer barrier made it wait).
func TestWorkflowReadyFiresWithoutLayerBarrier(t *testing.T) {
	f := &wfSlowFake{wfFake: *newWfFake(), slow: map[string]int{"sa-fake-wf-slowIndep": 4}}
	spec := WorkflowSpec{
		Synthesis: "fold",
		Steps: []WorkflowStep{
			{ID: "chainA", Task: "t"},
			{ID: "chainB", Task: "t", DependsOn: []string{"chainA"}},
			{ID: "slowIndep", Task: "t"},
		},
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)
	if rep.Err != "" {
		t.Fatalf("unexpected error: %s", rep.Err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.names) != 4 { // 3 workers + synthesis
		t.Fatalf("expected 4 launches, got %v", f.names)
	}
	// chainB launches at the poll right after chainA's terminal flip (poll 3),
	// while slowIndep is still draining its 4-poll slow budget: without the
	// ready-set fix the old layer barrier held chainB until slowIndep
	// finished (poll 6+). Position 3 in the launch order proves it fired early.
	found := false
	for i, n := range f.names {
		if n == "wf-chainB" {
			found = true
			if i != 2 {
				t.Fatalf("chainB should be the 3rd launch (after chainA completes), got order %v", f.names)
			}
		}
	}
	if !found {
		t.Fatalf("chainB never launched: %v", f.names)
	}
}

// TestWorkflowWideGraphQueuesBeyondCap (research sa-123): a 10-step wide
// graph exceeds workflowLayerCap; the old Validate rejected it, ready-set
// scheduling runs it by queuing.
func TestWorkflowWideGraphQueuesBeyondCap(t *testing.T) {
	f := newWfFake()
	spec := WorkflowSpec{Synthesis: "fold"}
	for i := 0; i < 10; i++ {
		spec.Steps = append(spec.Steps, WorkflowStep{ID: fmt.Sprintf("w%02d", i), Task: "t"})
	}
	if _, err := spec.Validate(); err != nil {
		t.Fatalf("wide graph should validate: %v", err)
	}
	rep := RunWorkflow(context.Background(), f, f, spec, wfPoll)
	if rep.Err != "" {
		t.Fatalf("unexpected error: %s", rep.Err)
	}
	if rep.Partial {
		t.Fatalf("unexpected partial run")
	}
	if got := f.launchCount(); got != 11 { // 10 workers + synthesis
		t.Fatalf("expected 11 launches, got %d", got)
	}
}
