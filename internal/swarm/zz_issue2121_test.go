package swarm

// #2121 regression:
//   - P1: SpawnTeammate fetched the team pointer, dropped m.mu, and
//     inserted under team.mu only - a concurrent DeleteTeam finishing in
//     between left the teammate on a detached team (invisible to every
//     manager API, alive to rootCancel: a ghost).
//   - P2: a late teammate_idle emit wrote results back AFTER
//     ShutdownTeammate's cleanup, leaving an entry no later sweep could
//     reach.
// Fixed by: insert under both locks in canonical order with an existence
// re-check; DeleteTeam's final phase sweeps late additions; emit stores
// results only for still-governed teammates.

import (
	"testing"
)

// P2 guard: results must not be written back for a teammate that no
// longer belongs to any team (ShutdownTeammate already cleaned it).
func TestLateIdleEmitDoesNotResurrectResults(t *testing.T) {
	m := &Manager{
		teams:   make(map[string]*Team),
		results: make(map[string]string),
	}
	// tm-ghost is in NO team.
	m.emit(Event{
		Type:       "teammate_idle",
		TeammateID: "tm-ghost",
		Result:     "late result",
	})
	m.mu.Lock()
	_, stored := m.results["tm-ghost"]
	m.mu.Unlock()
	if stored {
		t.Fatal("late idle emit must not resurrect results for an ungoverned teammate")
	}

	// A governed teammate still stores normally.
	team := &Team{ID: "t1", Teammates: map[string]*Teammate{"tm-live": {}}}
	m.teams["t1"] = team
	m.emit(Event{Type: "teammate_idle", TeammateID: "tm-live", Result: "ok"})
	m.mu.Lock()
	v := m.results["tm-live"]
	m.mu.Unlock()
	if v != "ok" {
		t.Fatalf("governed teammate result must store, got %q", v)
	}
}

// P1: spawning into a team that a concurrent delete already removed must
// fail with "team not found", not inject a ghost. The existence check is
// under m.mu; simulate the post-delete state directly (the race window
// itself is µs-scale and covered by the lock-order fix).
func TestSpawnIntoDeletedTeamFails(t *testing.T) {
	m := &Manager{
		teams:   make(map[string]*Team),
		results: make(map[string]string),
	}
	// Direct check of the guard's building block: a missing team errors.
	if _, err := m.SpawnTeammate("no-such-team", "w", "", nil); err == nil {
		t.Fatal("spawn into a missing team must error")
	}
}
