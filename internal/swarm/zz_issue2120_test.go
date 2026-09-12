package swarm

// #2120 regression: DeleteTeam waited for the runner done channels while
// HOLDING m.mu, but the runner exit path emits an event (emit takes m.mu)
// BEFORE close(done) - a cycle only the 5s timeout broke, freezing every
// manager API, hit deterministically by the normal path (a teammate
// mid-task emitting team_board_updated on exit). The fix moves the
// bounded wait OUTSIDE m.mu (CancelAll's lock discipline) with a
// re-lock identity check on the map delete.
//
// This test simulates the runner exit shape exactly: on cancel, the
// goroutine takes m.mu, closes done, releases - under the old code that
// required DeleteTeam's timeout to expire.

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
		// Simulate the runner exit path: on cancel, EMIT first (takes
		// m.mu) and only then close(done) - the exact ordering that
		// deadlocked the old DeleteTeam.
		<-runnerCtx.Done()
		close(started)
		m.mu.Lock()
		close(done)
		m.mu.Unlock()
	}()

	go func() {
		<-started
		// A concurrent manager API (e.g. GetTeam) must NOT be frozen for
		// the whole delete: under the old code this Lock queued behind
		// DeleteTeam's 5s hold.
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
