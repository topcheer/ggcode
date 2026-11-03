package swarm

import (
	"testing"
)

func TestLateIdleEmitDoesNotResurrectResults(t *testing.T) {
	m := &Manager{
		teams:   make(map[string]*Team),
		results: make(map[string]string),
	}

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

func TestSpawnIntoDeletedTeamFails(t *testing.T) {
	m := &Manager{
		teams:   make(map[string]*Team),
		results: make(map[string]string),
	}

	if _, err := m.SpawnTeammate("no-such-team", "w", "", nil); err == nil {
		t.Fatal("spawn into a missing team must error")
	}
}
