package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// --- fakes ---

type fakeSpawner struct {
	calls   []tool.LaunchOptions
	failAt  int // 1-based launch index that returns an error (0 = never)
	nextErr error
}

func (f *fakeSpawner) Launch(ctx context.Context, opts tool.LaunchOptions) (string, string, error) {
	f.calls = append(f.calls, opts)
	if f.failAt > 0 && len(f.calls) == f.failAt {
		return "", "", fmt.Errorf("git worktree boom")
	}
	return fmt.Sprintf("cand-%d", len(f.calls)), fmt.Sprintf("/tmp/wt-%d", len(f.calls)), nil
}

type fakeSnaps struct {
	m       map[string]subagent.Snapshot
	running int
}

func (f *fakeSnaps) Snapshot(id string) (subagent.Snapshot, bool) {
	s, ok := f.m[id]
	return s, ok
}

func (f *fakeSnaps) RunningCount() int { return f.running }

// fakeCancSnaps adds the optional CandidateCanceller capability and records
// which IDs the orchestrator asked to roll back (#3070).
type fakeCancSnaps struct {
	fakeSnaps
	cancelled []string
}

func (f *fakeCancSnaps) Cancel(id string) bool {
	f.cancelled = append(f.cancelled, id)
	return true
}

func doneSnap(id, result string, fail bool) subagent.Snapshot {
	st := subagent.StatusCompleted
	evs := []subagent.AgentEvent{
		{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: `{"path":"a.go"}`},
	}
	if fail {
		st = subagent.StatusFailed
		evs = append(evs,
			subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "edit_file", Result: "anchor not found", IsError: true},
		)
		return subagent.Snapshot{ID: id, Status: st, Error: "edit failed: anchor not found", Events: evs}
	}
	evs = append(evs,
		subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "edit_file", Result: "ok"},
	)
	return subagent.Snapshot{ID: id, Status: st, Result: result, Events: evs}
}

// --- tests ---

func TestRunBestOfN_PicksConsensusWinner(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok: edit applied, tests pass", false),
		"cand-2": doneSnap("cand-2", "", true),
		"cand-3": doneSnap("cand-3", "ok: edit applied, tests pass", false),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "fix bug X", N: 3, Name: "test-bo-n", Poll: time.Millisecond})
	if rep.Err != "" {
		t.Fatalf("unexpected orchestration error: %s", rep.Err)
	}
	if len(sp.calls) != 3 {
		t.Fatalf("expected 3 launches, got %d", len(sp.calls))
	}
	if rep.WinnerIndex < 0 {
		t.Fatalf("expected a winner, got none (Degraded=%v)", rep.Degraded)
	}
	if got := rep.Candidates[rep.WinnerIndex].Verdict; got != "succeeded" {
		t.Fatalf("winner verdict = %q, want succeeded", got)
	}
	if rep.Degraded {
		t.Fatal("consensus case must not degrade")
	}
	if !strings.Contains(rep.Report, "Winner") || !strings.Contains(rep.Report, "/tmp/wt-") {
		t.Fatalf("report should surface the winner and its worktree, got:\n%s", rep.Report)
	}
	// Every candidate got the independence + verify-yourself suffix.
	for _, c := range sp.calls {
		if !strings.Contains(c.Task, "INDEPENDENT parallel candidates") {
			t.Fatalf("candidate task missing suffix: %q", c.Task)
		}
	}
}

func TestRunBestOfN_ClampsToFour(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{}}
	for i := 1; i <= 4; i++ {
		sn.m[fmt.Sprintf("cand-%d", i)] = doneSnap(fmt.Sprintf("cand-%d", i), "ok: tests pass", false)
	}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 9, Poll: time.Millisecond})
	if rep.N != 4 || len(sp.calls) != 4 {
		t.Fatalf("expected clamp to 4, got N=%d launches=%d", rep.N, len(sp.calls))
	}
}

func TestRunBestOfN_SlotGateRefuses(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{running: 14}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 4})
	if rep.Err == "" || !strings.Contains(rep.Err, "free sub-agent slots") {
		t.Fatalf("expected slot-gate refusal, got: %q", rep.Err)
	}
	if len(sp.calls) != 0 {
		t.Fatalf("gate must refuse before launching, launched %d", len(sp.calls))
	}
}

func TestRunBestOfN_RejectsSubTwo(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 1})
	if rep.Err == "" {
		t.Fatal("n=1 must be rejected")
	}
	rep = RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "  "})
	if rep.Err == "" {
		t.Fatal("empty task must be rejected")
	}
}

func TestRunBestOfN_LaunchFailureAbortsCleanly(t *testing.T) {
	sp := &fakeSpawner{failAt: 2}
	sn := &fakeSnaps{} // no Cancel capability: IDs must still be exposed
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 2})
	if rep.Err == "" || !strings.Contains(rep.Err, "candidate 2/2") {
		t.Fatalf("expected launch-failure abort naming the candidate, got: %q", rep.Err)
	}
	if !strings.Contains(rep.Report, "Aborted after 1/2") {
		t.Fatalf("report should state partial fan-out, got: %q", rep.Report)
	}
	// #3070: even without a canceller, the launched candidate must be
	// exposed (id + worktree) so the parent can clean up, not leaked.
	if len(rep.Candidates) != 1 {
		t.Fatalf("expected 1 exposed candidate, got %d", len(rep.Candidates))
	}
	if c := rep.Candidates[0]; c.ID != "cand-1" || c.Status != "orphaned" || c.Worktree != "/tmp/wt-1" {
		t.Fatalf("exposed candidate wrong: %+v", c)
	}
	if !strings.Contains(rep.Report, "cand-1") {
		t.Fatalf("report must surface the orphaned id, got: %q", rep.Report)
	}
}

// TestRunBestOfN_LaunchFailureCancelsLaunchedCandidates: when the snapshot
// source supports cancellation, a mid-fan-out launch failure rolls back
// every already-launched candidate instead of leaking goroutines/worktrees.
func TestRunBestOfN_LaunchFailureCancelsLaunchedCandidates(t *testing.T) {
	sp := &fakeSpawner{failAt: 3} // candidates 1,2 launch; 3 fails
	sn := &fakeCancSnaps{fakeSnaps: fakeSnaps{}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 3})
	if rep.Err == "" || !strings.Contains(rep.Err, "candidate 3/3") {
		t.Fatalf("expected abort on candidate 3, got: %q", rep.Err)
	}
	if len(sn.cancelled) != 2 || sn.cancelled[0] != "cand-1" || sn.cancelled[1] != "cand-2" {
		t.Fatalf("candidates 1,2 must be cancelled, got: %v", sn.cancelled)
	}
	for _, c := range rep.Candidates {
		if c.Status != string(subagent.StatusCancelled) {
			t.Fatalf("candidate %s should be recorded cancelled, got %q", c.ID, c.Status)
		}
	}
	if !strings.Contains(rep.Report, "cancelled") {
		t.Fatalf("report should mention cancelled candidates, got: %q", rep.Report)
	}
}

func TestRunBestOfN_PartialOnCallerCancel(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": {ID: "cand-1", Status: subagent.StatusRunning},
		"cand-2": {ID: "cand-2", Status: subagent.StatusRunning},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	rep := RunBestOfN(ctx, sp, sn, BestOfNOptions{Task: "t", N: 2, Poll: 5 * time.Millisecond})
	if !rep.Partial {
		t.Fatal("caller cancel must produce a partial report")
	}
	if rep.WinnerIndex >= 0 {
		t.Fatal("partial report must not pick a winner")
	}
	if !strings.Contains(rep.Report, "wait_agent") {
		t.Fatalf("partial report should point at wait_agent, got: %q", rep.Report)
	}
	for _, c := range rep.Candidates {
		if c.Status != "running" {
			t.Fatalf("candidates must be reported as still running, got %q", c.Status)
		}
	}
}

func TestRunBestOfN_DegradedWhenNoConsensus(t *testing.T) {
	sp := &fakeSpawner{}
	// Two candidates failing DIFFERENT ways: no majority winner.
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "", true),
		"cand-2": {ID: "cand-2", Status: subagent.StatusFailed, Error: "build timeout",
			Events: []subagent.AgentEvent{{Type: subagent.AgentEventError, Text: "build timeout"}}},
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 2, Poll: time.Millisecond})
	if rep.Err != "" {
		t.Fatalf("unexpected error: %s", rep.Err)
	}
	if !rep.Degraded || rep.WinnerIndex >= 0 {
		t.Fatalf("two distinct failures must degrade, got Degraded=%v WinnerIndex=%d", rep.Degraded, rep.WinnerIndex)
	}
	if rep.ConditioningHint == "" {
		t.Fatal("degraded path must distill a conditioning hint")
	}
	if !strings.Contains(rep.Report, "Sequential-retry conditioning") {
		t.Fatalf("report should explain the degradation, got: %q", rep.Report)
	}
}

func TestBestOfNRunnerForBridgesToolShell(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok: tests pass", false),
		"cand-2": doneSnap("cand-2", "", true),
	}}
	run := BestOfNRunnerFor(sp, sn)
	got := run(context.Background(), tool.BestOfNRequest{Task: "task", N: 2, Isolation: "worktree", Name: "lbl"})
	if !strings.Contains(got, "Winner") && !strings.Contains(got, "Sequential-retry conditioning") {
		t.Fatalf("runner must return the report text, got: %q", got)
	}
}

// r380: per-candidate model mapping - each candidate gets its assigned
// model; with no Models configured every candidate inherits (Model="").
func TestRunBestOfN_PerCandidateModels(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok: tests pass", false),
		"cand-2": doneSnap("cand-2", "ok: tests pass", false),
		"cand-3": doneSnap("cand-3", "", true),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{
		Task: "t", N: 3, Models: []string{"cheap-air", "flagship"},
	})
	if rep.Err != "" {
		t.Fatalf("unexpected err: %s", rep.Err)
	}
	want := []string{"cheap-air", "flagship", "cheap-air"} // cycles when models < n
	for i, call := range sp.calls {
		if call.Model != want[i] {
			t.Errorf("candidate %d: Model=%q, want %q", i+1, call.Model, want[i])
		}
	}
}

// r380: same-model default is preserved - no Models means every Launch
// carries Model="" (parent runtime model inheritance downstream).
func TestRunBestOfN_NoModelsInheritsParent(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok", false),
		"cand-2": doneSnap("cand-2", "ok", false),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "t", N: 2})
	if rep.Err != "" {
		t.Fatalf("unexpected err: %s", rep.Err)
	}
	for i, call := range sp.calls {
		if call.Model != "" {
			t.Errorf("candidate %d: Model=%q, want empty (inherit)", i+1, call.Model)
		}
	}
}

// r478 probes: shared-error correlation sentinel. When all successful
// candidates return near-identical results, consensus ranking gets a
// high-confidence winner that may simply be the SAME mistake everywhere -
// the sentinel must flag it instead of staying silent.
func TestRunBestOfN_FlagsSharedErrorCorrelation(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok: edit applied to parser.go, tests pass", false),
		"cand-2": doneSnap("cand-2", "", true),
		"cand-3": doneSnap("cand-3", "ok: edit applied to parser.go, tests pass", false),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "fix bug X", N: 3, Name: "shared-err", Poll: time.Millisecond})
	if rep.Err != "" {
		t.Fatalf("unexpected orchestration error: %s", rep.Err)
	}
	if rep.WinnerIndex < 0 {
		t.Fatalf("expected a consensus winner, got none (Degraded=%v)", rep.Degraded)
	}
	if !rep.HighCorrelation {
		t.Fatalf("identical successful results must set HighCorrelation (sim=%.2f)", rep.AvgSimilarity)
	}
	if rep.AvgSimilarity < bonSharedErrorSimWarn {
		t.Fatalf("sim=%.2f, want >= %.2f", rep.AvgSimilarity, bonSharedErrorSimWarn)
	}
	if !strings.Contains(rep.Report, "near-identical") || !strings.Contains(rep.Report, "shared error") {
		t.Fatalf("report must carry the shared-error warning, got:\n%s", rep.Report)
	}
}

func TestRunBestOfN_DistinctResultsNoCorrelationFlag(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok: fixed the race in the writer by adding a mutex around the flush path, go test ./internal/store/ green", false),
		"cand-2": doneSnap("cand-2", "ok: replaced the shared map with a channel-based pipeline so each stage owns its state, build passes and stress test clean", false),
		"cand-3": doneSnap("cand-3", "ok: split the single goroutine into per-key workers with bounded semaphore, tests pass with -race on", false),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{Task: "fix bug X", N: 3, Name: "distinct", Poll: time.Millisecond})
	if rep.Err != "" {
		t.Fatalf("unexpected orchestration error: %s", rep.Err)
	}
	if rep.HighCorrelation {
		t.Fatalf("distinct results must not flag correlation (sim=%.2f)", rep.AvgSimilarity)
	}
	if strings.Contains(rep.Report, "near-identical") {
		t.Fatalf("no shared-error warning expected, got:\n%s", rep.Report)
	}
}

// Pure-function edges: fewer than two results is not a correlation signal.
func TestPairwiseResultSimilarityEdges(t *testing.T) {
	if _, ok := pairwiseResultSimilarity(nil); ok {
		t.Fatal("nil results must not produce a similarity")
	}
	if _, ok := pairwiseResultSimilarity([]string{"only one"}); ok {
		t.Fatal("single result must not produce a similarity")
	}
	sim, ok := pairwiseResultSimilarity([]string{"alpha beta gamma delta epsilon zeta", "alpha beta gamma delta epsilon zeta"})
	if !ok || sim != 1.0 {
		t.Fatalf("identical results => sim=1.0, got %.2f ok=%v", sim, ok)
	}
	// Failed candidates never feed the similarity estimate.
	got := successfulResults([]CandidateOutcome{
		{Status: string(subagent.StatusCompleted), Result: "r1"},
		{Status: "failed", Result: "same text"},
		{Status: string(subagent.StatusCompleted), Result: ""},
	})
	if len(got) != 1 || got[0] != "r1" {
		t.Fatalf("successfulResults must keep only non-empty successes, got %v", got)
	}
}
