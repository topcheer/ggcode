package swarm

import (
	"context"
	"testing"
	"time"
)

func TestDeleteTeamDoesNotHoldManagerLockWhileWaiting(t *testing.T) {
	m := &Manager{
		teams:   make(map[string]*Team),
		results: make(map[string]string),
	}
	team := &Team{
		ID:        "t-2120",
		Name:      "t",
		Teammates: make(map[string]*Teammate),
	}
	m.teams[team.ID] = team

	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	tm := &Teammate{
		ID:     "tm-2120",
		Name:   "worker",
		ctx:    runnerCtx,
		cancel: runnerCancel,
		done:   done,
	}
	team.Teammates[tm.ID] = tm

	started := make(chan struct{})
	go func() {

		<-runnerCtx.Done()
		close(started)
		m.mu.Lock()
		close(done)
		m.mu.Unlock()
	}()

	go func() {
		<-started

		m.mu.Lock()
		m.mu.Unlock()
	}()

	begin := time.Now()
	if err := m.DeleteTeam(team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	elapsed := time.Since(begin)
	if elapsed > 3*time.Second {
		t.Fatalf("DeleteTeam took %v - the m.mu-held wait cycle is back (timeout-broken at ~5s)", elapsed)
	}
	m.mu.Lock()
	_, still := m.teams[team.ID]
	m.mu.Unlock()
	if still {
		t.Fatal("team must be removed from the map")
	}
}
