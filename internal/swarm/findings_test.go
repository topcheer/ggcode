package swarm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

type quietAgent struct{}

func (quietAgent) RunStream(_ context.Context, _ string, _ func(provider.StreamEvent), origin ...string) error {
	return nil
}

type longAgent struct{ runes int }

func (a longAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent), origin ...string) error {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: strings.Repeat("r", a.runes)})
	}
	return nil
}

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
	parkedBlocked := board.Create("after parked dep", "d", "", nil)
	if _, err := board.Update(parkedBlocked.ID, task.UpdateOptions{AddBlockedBy: []string{failed.ID}}); err != nil {
		t.Fatalf("add parked dep: %v", err)
	}
	emptyBlocked := board.Create("after silent dep", "d", "", nil)
	if _, err := board.Update(emptyBlocked.ID, task.UpdateOptions{AddBlockedBy: []string{empty.ID}}); err != nil {
		t.Fatalf("add empty dep: %v", err)
	}

	agent := &countingAgent{}
	tryClaimPendingTask(context.Background(), tm, teamOf(mgr, t), agent, mgr, nil, time.Second)

	prompts := agent.getPrompts()

	if len(prompts) != 1 {
		t.Fatalf("expected exactly 1 claim (empty-dep dependent only; parked-dep dependent must stay blocked), got %d prompts", len(prompts))
	}
	if !strings.Contains(prompts[0], "after silent dep") {
		t.Fatalf("expected the empty-dep dependent to be the claimed task:\n%s", prompts[0])
	}
	if strings.Contains(prompts[0], "Findings from completed dependency tasks") {
		t.Fatalf("deps without persisted results must not open a findings section:\n%s", prompts[0])
	}
}

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
	if got := len([]rune(section)); got > depFindingsMaxTotal+200 {
		t.Fatalf("findings section exceeds total bound: %d runes", got)
	}

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
