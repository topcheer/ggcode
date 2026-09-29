package swarm

// #2787 regression: ShutdownTeammate unconditionally deleted
// m.results[tmID], contradicting #1814 ("shut the worker down, THEN
// collect the output" is the most common leader order) and the emit
// comment "Keep the last result available even after shutdown". The
// delete was terminal data loss: no path writes the result back after
// removal (#2121 late-emit guard drops ungoverned writes).
//
// Reclamation is NOT per-shutdown (pruning ungoverned entries there would
// also delete OTHER teammates' not-yet-collected results): entries are
// reclaimed by DeleteTeam's sweep plus the FIFO bound in
// storeResultLocked (#1633 leak stays fixed).

import (
	"fmt"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Guard probe: the stored result must survive the REAL shutdown path.
func TestIssue2787_ResultSurvivesShutdownTeammate(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2787", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-1", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the idle loop start

	// Stored final result, exactly what the emit path writes on
	// teammate_idle.
	m.mu.Lock()
	m.results[tm.ID] = "final output"
	m.mu.Unlock()

	if err := m.ShutdownTeammate(team.ID, tm.ID); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	r, ok := m.GetTeammateResult(team.ID, tm.ID)
	if !ok || r != "final output" {
		t.Fatalf("#2787: stored result must survive ShutdownTeammate (the #1814 order), got (%q, %v)", r, ok)
	}
}

// Guard probe: an UNRELATED later shutdown must not sweep a peer's
// stored-but-not-yet-collected result. (The first cut of this fix pruned
// all ungoverned entries on every shutdown - A shut down uncollected, B
// shut down next, A's output was lost.)
func TestIssue2787_UncollectedPeerSurvivesUnrelatedShutdown(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2787-peer", "leader-1")

	tmA, err := m.SpawnTeammate(team.ID, "worker-a", "", nil)
	if err != nil {
		t.Fatalf("spawn A: %v", err)
	}
	m.mu.Lock()
	m.results[tmA.ID] = "A output"
	m.mu.Unlock()
	if err := m.ShutdownTeammate(team.ID, tmA.ID); err != nil {
		t.Fatalf("shutdown A: %v", err)
	}

	// Unrelated second teammate completes and is shut down BEFORE the
	// leader ever collected A's result.
	tmB, err := m.SpawnTeammate(team.ID, "worker-b", "", nil)
	if err != nil {
		t.Fatalf("spawn B: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	m.mu.Lock()
	m.results[tmB.ID] = "B output"
	m.mu.Unlock()
	if err := m.ShutdownTeammate(team.ID, tmB.ID); err != nil {
		t.Fatalf("shutdown B: %v", err)
	}

	r, ok := m.GetTeammateResult(team.ID, tmA.ID)
	if !ok || r != "A output" {
		t.Fatalf("#2787: uncollected peer result must survive an unrelated shutdown, got (%q, %v)", r, ok)
	}
}

// Guard probe: bounded retention - the #1633 leak must not return. The
// FIFO bound in storeResultLocked evicts the oldest entries once
// maxStoredResults is exceeded.
func TestIssue2787_ResultsFIFOBound(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	m.mu.Lock()
	for i := 0; i < maxStoredResults+10; i++ {
		m.storeResultLocked(fmt.Sprintf("tm-%03d", i), fmt.Sprintf("r%d", i))
	}
	got := len(m.results)
	newest := fmt.Sprintf("tm-%03d", maxStoredResults+9)
	_, okNew := m.results[newest]
	_, okOld := m.results["tm-000"]
	m.mu.Unlock()

	if got != maxStoredResults {
		t.Fatalf("#2787: FIFO bound must cap the store at %d entries, got %d", maxStoredResults, got)
	}
	if !okNew {
		t.Fatalf("#2787: newest entry %s must survive the bound", newest)
	}
	if okOld {
		t.Fatal("#2787: oldest entry tm-000 must be evicted by the FIFO bound")
	}
}

// Re-writing an existing key must not duplicate its FIFO position.
func TestIssue2787_ResultsUpdateKeepsFIFOPosition(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	m.mu.Lock()
	m.storeResultLocked("tm-a", "first")
	m.storeResultLocked("tm-b", "b1")
	m.storeResultLocked("tm-a", "second")
	stored, okA := m.results["tm-a"]
	n := len(m.results)
	orderLen := len(m.resultsOrder)
	m.mu.Unlock()

	if !okA || stored != "second" {
		t.Fatalf("#2787: re-store must update the value, got (%q, %v)", stored, okA)
	}
	if n != 2 || orderLen != 2 {
		t.Fatalf("#2787: re-store must not add FIFO positions: map=%d order=%d", n, orderLen)
	}
}

// Guard probe: the team lifecycle sweep still reclaims entries
// (DeleteTeam) - the primary reclamation path for teams that do get
// deleted.
func TestIssue2787_DeleteTeamStillReclaims(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2787-del", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-del", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.mu.Lock()
	m.storeResultLocked(tm.ID, "out")
	m.mu.Unlock()

	if err := m.DeleteTeam(team.ID); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	if r, ok := m.GetTeammateResult(team.ID, tm.ID); ok || r != "" {
		t.Fatalf("#2787: DeleteTeam must reclaim the stored result entry, got (%q, %v)", r, ok)
	}
}
