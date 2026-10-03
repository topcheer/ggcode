package swarm

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

// r456: priority-aware claiming + starvation detection probes.

func mkTask(id, priority string, created time.Time) task.Task {
	return task.Task{ID: id, Subject: id, Metadata: map[string]string{"priority": priority}, CreatedAt: created}
}

// Priority metadata ranks: high > medium/unknown > low.
func TestEffectivePriority_Ranking(t *testing.T) {
	now := time.Now()
	high := effectivePriority(mkTask("h", "high", now), now)
	med := effectivePriority(mkTask("m", "medium", now), now)
	low := effectivePriority(mkTask("l", "low", now), now)
	absent := effectivePriority(mkTask("a", "", now), now)
	if !(high > med && med > low) {
		t.Fatalf("rank broken: high=%d med=%d low=%d", high, med, low)
	}
	if med != absent {
		t.Fatalf("absent priority must rank as medium: %d vs %d", absent, med)
	}
}

// Aging lets an old low task outrank a fresh medium task (~bounded).
func TestEffectivePriority_Aging(t *testing.T) {
	now := time.Now()
	oldLow := effectivePriority(mkTask("ol", "low", now.Add(-60*time.Minute)), now)
	freshMed := effectivePriority(mkTask("fm", "medium", now), now)
	if oldLow <= freshMed {
		t.Fatalf("60min-old low (%d) must outrank fresh medium (%d)", oldLow, freshMed)
	}
	// But a fresh high still beats a 10min-old medium.
	freshHigh := effectivePriority(mkTask("fh", "high", now), now)
	slightlyAgedMed := effectivePriority(mkTask("sm", "medium", now.Add(-10*time.Minute)), now)
	if freshHigh <= slightlyAgedMed {
		t.Fatalf("fresh high (%d) must beat 10min medium (%d)", freshHigh, slightlyAgedMed)
	}
	// Zero/negative clocks (defensive) yield no bonus.
	if b := agingBonus(time.Time{}, now); b != 0 {
		t.Fatalf("zero CreatedAt must yield 0 bonus, got %d", b)
	}
}

// sortClaimable: high-first; ties stable (list order preserved).
func TestSortClaimable_OrderAndStability(t *testing.T) {
	now := time.Now()
	ts := []task.Task{
		mkTask("first-med", "medium", now),
		mkTask("low", "low", now),
		mkTask("second-med", "medium", now),
		mkTask("high", "high", now),
	}
	sortClaimable(ts, now)
	if ts[0].ID != "high" {
		t.Fatalf("high must claim first, got %s", ts[0].ID)
	}
	if ts[1].ID != "first-med" || ts[2].ID != "second-med" {
		t.Fatalf("ties must keep list order (stable), got %s then %s", ts[1].ID, ts[2].ID)
	}
	if ts[3].ID != "low" {
		t.Fatalf("low must claim last, got %s", ts[3].ID)
	}
}

// No priority metadata anywhere: order unchanged (FIFO behavior preserved).
func TestSortClaimable_NoMetadataIsFIFO(t *testing.T) {
	now := time.Now()
	ts := []task.Task{
		mkTask("a", "", now),
		mkTask("b", "", now),
		mkTask("c", "", now),
	}
	sortClaimable(ts, now)
	for i, want := range []string{"a", "b", "c"} {
		if ts[i].ID != want {
			t.Fatalf("FIFO broken: pos %d got %s want %s", i, ts[i].ID, want)
		}
	}
}

// markStarved: fires once for an old claimable task, writes marker, and
// the marker suppresses the second alert.
func TestMarkStarved_OneShot(t *testing.T) {
	old := starvationThreshold
	starvationThreshold = time.Nanosecond // any task instantly "old"
	defer func() { starvationThreshold = old }()

	tmMgr := task.NewManager() // in-memory board
	tk := tmMgr.Create("old task", "waiting", "active", map[string]string{"priority": "low"})
	// Age it: CreatedAt is set at creation; shrink the threshold above
	// makes a fresh task old enough after we backdate via Update.
	past := time.Now().Add(-2 * time.Minute).Format(time.RFC3339)
	_ = past

	// Backdate directly through the manager (task.Manager keeps CreatedAt
	// at creation; simplest honest probe: threshold so small that even a
	// fresh task is "old").
	fresh := tmMgr.List()
	if len(fresh) != 1 {
		t.Fatalf("board must hold 1 task, got %d", len(fresh))
	}
	markStarved(tmMgr, fresh[0], time.Now())
	got, ok := tmMgr.Get(tk.ID)
	if !ok || got.Metadata[starvedSinceKey] == "" {
		t.Fatal("first markStarved must write the marker")
	}
	// Second call must not rewrite (one-shot).
	first := got.Metadata[starvedSinceKey]
	time.Sleep(10 * time.Millisecond)
	markStarved(tmMgr, got, time.Now())
	again, _ := tmMgr.Get(tk.ID)
	if again.Metadata[starvedSinceKey] != first {
		t.Fatal("marker must be one-shot")
	}
}

// markStarved: young tasks are not marked.
func TestMarkStarved_YoungTaskUntouched(t *testing.T) {
	tmMgr := task.NewManager()
	tk := tmMgr.Create("young", "fresh", "active", nil)
	markStarved(tmMgr, mustGet(t, tmMgr, tk.ID), time.Now())
	got := mustGet(t, tmMgr, tk.ID)
	if _, marked := got.Metadata[starvedSinceKey]; marked {
		t.Fatal("young task must not be marked")
	}
}

func mustGet(t *testing.T, tmMgr *task.Manager, id string) task.Task {
	t.Helper()
	tk, ok := tmMgr.Get(id)
	if !ok {
		t.Fatalf("task %s missing", id)
	}
	return tk
}
