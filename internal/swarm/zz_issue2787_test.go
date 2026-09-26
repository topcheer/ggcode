package swarm

// #2787 regression: ShutdownTeammate unconditionally deleted
// m.results[tmID], contradicting #1814 ("shut the worker down, THEN
// collect the output" is the most common leader order) and the emit
// comment "Keep the last result available even after shutdown". The
// delete was terminal data loss: no path writes the result back after
// removal (#2121 late-emit guard drops ungoverned writes).

import (
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

// Guard probe: bounded retention - the #1633 leak must not return.
// Entries whose teammate belongs to no live team are pruned by the next
// shutdown, so repeated spawn/shutdown cycles without DeleteTeam cannot
// grow m.results without bound.
func TestIssue2787_StaleEntriesPrunedOnShutdown(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2787b", "leader-1")

	// Stale entry from an earlier shutdown cycle: teammate gone from
	// every team, result never collected.
	m.mu.Lock()
	m.results["tm-ghost"] = "never collected"
	m.mu.Unlock()

	tm, err := m.SpawnTeammate(team.ID, "worker-2", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	m.mu.Lock()
	m.results[tm.ID] = "fresh output"
	m.mu.Unlock()

	if err := m.ShutdownTeammate(team.ID, tm.ID); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	m.mu.Lock()
	_, ghostKept := m.results["tm-ghost"]
	fresh, freshKept := m.results[tm.ID]
	m.mu.Unlock()
	if ghostKept {
		t.Fatal("#2787: stale ungoverned entry must be pruned on shutdown (#1633 bound)")
	}
	if !freshKept || fresh != "fresh output" {
		t.Fatalf("#2787: just-shutdown teammate's result must be retained, got (%q, %v)", fresh, freshKept)
	}
}
