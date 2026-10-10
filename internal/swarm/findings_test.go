package swarm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// quietAgent emits no text output (used to pin the empty-result path).
type quietAgent struct{}

func (quietAgent) RunStream(_ context.Context, _ string, _ func(provider.StreamEvent)) error {
	return nil
}

// longAgent emits a result of the given rune length.
type longAgent struct{ runes int }

func (a longAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: strings.Repeat("r", a.runes)})
	}
	return nil
}

// newClaimHarness builds a manager + team + teammate like TestRollbackClaimedTask.
func newClaimHarness(t *testing.T) (*Manager, *task.Manager, *Teammate) {
	t.Helper()
	mgr := newTestManager()
	snap := mgr.CreateTeam("dep-blackboard", "leader")
	t.Cleanup(func() { _ = mgr.DeleteTeam(snap.ID) })
	mgr.mu.Lock()
	team := mgr.teams[snap.ID]
	mgr.mu.Unlock()
	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("ensure board: %v", err)
	}
	return mgr, board, &Teammate{ID: "tm-1"}
}

// TestPersistTaskResult_OnCompletion pins the write side of the dep-chain
// blackboard: when a teammate completes a board task, the final output is
// persisted on the task's metadata so later dependent claims can read it.
func TestPersistTaskResult_OnCompletion(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	created := board.Create("produce findings", "d", "", nil)

	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), &countingAgent{}, mgr, nil, time.Second)

	got, ok := board.Get(created.ID)
	if !ok || got.Status != task.StatusCompleted {
		t.Fatalf("task must be completed, got %+v", got)
	}
	result := got.Metadata[resultMetaKey]
	if !strings.Contains(result, "result for:") {
		t.Fatalf("result metadata missing agent output, got %q", result)
	}
}

// TestPersistTaskResult_Truncated pins the board-side bound (2000 runes).
func TestPersistTaskResult_Truncated(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	created := board.Create("long output", "d", "", nil)

	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), longAgent{runes: boardResultMaxRunes + 500}, mgr, nil, time.Second)

	got, _ := board.Get(created.ID)
	result := got.Metadata[resultMetaKey]
	if len([]rune(result)) != boardResultMaxRunes {
		t.Fatalf("result must be truncated to %d runes, got %d", boardResultMaxRunes, len([]rune(result)))
	}
}

// TestPersistTaskResult_EmptySkipped pins that empty output writes nothing.
func TestPersistTaskResult_EmptySkipped(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	created := board.Create("silent", "d", "", nil)

	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), quietAgent{}, mgr, nil, time.Second)

	got, _ := board.Get(created.ID)
	if got.Status != task.StatusCompleted {
		t.Fatalf("task must still complete, got %s", got.Status)
	}
	if _, ok := got.Metadata[resultMetaKey]; ok {
		t.Fatalf("empty output must not write %s metadata", resultMetaKey)
	}
}

// TestDepFindingsInjectedIntoClaimPrompt pins the read side: a claimed task
// whose dependency completed with a persisted result receives the finding
// in its prompt.
func TestDepFindingsInjectedIntoClaimPrompt(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	dep := board.Create("scan module", "d", "", nil)
	completed := task.StatusCompleted
	if _, err := board.Update(dep.ID, task.UpdateOptions{
		Status:   &completed,
		Metadata: map[string]string{resultMetaKey: "found 3 call sites in api.go"},
	}); err != nil {
		t.Fatalf("complete dep: %v", err)
	}
	blocked := board.Create("fix call sites", "d", "", nil)
	if _, err := board.Update(blocked.ID, task.UpdateOptions{AddBlockedBy: []string{dep.ID}}); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	prompts := agent.getPrompts()
	if len(prompts) != 1 {
		t.Fatalf("expected 1 claim prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if !strings.Contains(p, "Findings from completed dependency tasks") {
		t.Fatalf("claim prompt missing dep-findings section:\n%s", p)
	}
	if !strings.Contains(p, "["+dep.ID+"] scan module: found 3 call sites in api.go") {
		t.Fatalf("claim prompt missing dep finding entry:\n%s", p)
	}
	if !strings.Contains(p, "fix call sites") {
		t.Fatalf("claim prompt missing the claimed task itself:\n%s", p)
	}
}

// TestDepFindingsSkipsFailedAndEmptyDeps pins exclusion semantics: deps
// that completed without a persisted result (e.g. parked with
// permanent_error) contribute no findings section.
func TestDepFindingsSkipsFailedAndEmptyDeps(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	failed := board.Create("poison task", "d", "", nil)
	completed := task.StatusCompleted
	if _, err := board.Update(failed.ID, task.UpdateOptions{
		Status:   &completed,
		Metadata: map[string]string{"permanent_error": "quota", "error": "boom"},
	}); err != nil {
		t.Fatalf("park dep: %v", err)
	}
	empty := board.Create("silent dep", "d", "", nil)
	if _, err := board.Update(empty.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatalf("complete dep: %v", err)
	}
	blocked := board.Create("after mixed deps", "d", "", nil)
	if _, err := board.Update(blocked.ID, task.UpdateOptions{AddBlockedBy: []string{failed.ID, empty.ID}}); err != nil {
		t.Fatalf("add deps: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	prompts := agent.getPrompts()
	if len(prompts) != 1 {
		t.Fatalf("expected claim of the dependent task, got %d prompts", len(prompts))
	}
	if strings.Contains(prompts[0], "Findings from completed dependency tasks") {
		t.Fatalf("deps without persisted results must not open a findings section:\n%s", prompts[0])
	}
}

// TestDepFindingsUnmetDepsStillBlocked pins that the injection does not
// weaken the ordering gate: a dep still in progress (owned elsewhere)
// prevents the dependent task from being claimed.
func TestDepFindingsUnmetDepsStillBlocked(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	dep := board.Create("not done yet", "d", "", nil)
	inProgress := task.StatusInProgress
	owner := "tm-2"
	if _, err := board.Update(dep.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatalf("start dep: %v", err)
	}
	blocked := board.Create("must wait", "d", "", nil)
	if _, err := board.Update(blocked.ID, task.UpdateOptions{AddBlockedBy: []string{dep.ID}}); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	if agent.getCalls() != 0 {
		t.Fatalf("unmet dependency must prevent claim, agent ran %d times", agent.getCalls())
	}
}

// TestDepFindingsBounds pins the injection bounds. With max-length
// results the 4800-rune total binds first (5 entries of ~820 runes);
// with short results the 6-entry cap binds. Per-entry truncation to
// 800 runes is asserted in both.
func TestDepFindingsBounds(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	completed := task.StatusCompleted

	var depIDs []string
	for i := 0; i < 8; i++ {
		dep := board.Create("dep", "d", "", nil)
		if _, err := board.Update(dep.ID, task.UpdateOptions{
			Status:   &completed,
			Metadata: map[string]string{resultMetaKey: strings.Repeat("f", depFindingMaxRunes+100)},
		}); err != nil {
			t.Fatalf("dep %d: %v", i, err)
		}
		depIDs = append(depIDs, dep.ID)
	}
	blocked := board.Create("big merge", "d", "", nil)
	if _, err := board.Update(blocked.ID, task.UpdateOptions{AddBlockedBy: depIDs}); err != nil {
		t.Fatalf("add deps: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	prompts := agent.getPrompts()
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if strings.Count(p, strings.Repeat("f", depFindingMaxRunes+100)) > 0 {
		t.Fatalf("per-entry truncation to %d runes was not applied", depFindingMaxRunes)
	}
	idx := strings.Index(p, "Findings from completed dependency tasks")
	if idx < 0 {
		t.Fatalf("findings section missing")
	}
	section := p[idx:]
	if got := len([]rune(section)); got > depFindingsMaxTotal+200 { // +200 slack for headers/subjects
		t.Fatalf("findings section exceeds total bound: %d runes", got)
	}
	// With ~820-rune entries, the 4800 total bound allows 5 entries.
	entries := 0
	for _, id := range depIDs {
		if strings.Contains(p, "["+id+"]") {
			entries++
		}
	}
	if entries < 1 || entries > maxDepFindings {
		t.Fatalf("expected 1..%d dep entries, got %d", maxDepFindings, entries)
	}
}

// TestDepFindingsEntryCap pins the 6-entry cap when results are short
// enough that the total bound does not bind first.
func TestDepFindingsEntryCap(t *testing.T) {
	mgr, board, tm := newClaimHarness(t)
	completed := task.StatusCompleted

	var depIDs []string
	for i := 0; i < 8; i++ {
		dep := board.Create("dep", "d", "", nil)
		if _, err := board.Update(dep.ID, task.UpdateOptions{
			Status:   &completed,
			Metadata: map[string]string{resultMetaKey: "short finding"},
		}); err != nil {
			t.Fatalf("dep %d: %v", i, err)
		}
		depIDs = append(depIDs, dep.ID)
	}
	blocked := board.Create("merge", "d", "", nil)
	if _, err := board.Update(blocked.ID, task.UpdateOptions{AddBlockedBy: depIDs}); err != nil {
		t.Fatalf("add deps: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	p := agent.getPrompts()[0]
	entries := 0
	for _, id := range depIDs {
		if strings.Contains(p, "["+id+"]") {
			entries++
		}
	}
	if entries != maxDepFindings {
		t.Fatalf("expected exactly %d dep entries, got %d", maxDepFindings, entries)
	}
}

// teamOf extracts the live *Team pointer for direct idle-runner calls.
func teamOf(mgr *Manager, t *testing.T) *Team {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, team := range mgr.teams {
		return team
	}
	t.Fatalf("no team on manager")
	return nil
}
