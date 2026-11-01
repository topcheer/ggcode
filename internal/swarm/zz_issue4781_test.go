package swarm

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// #4781 (S-10): broadcast-style claiming produced 7 concurrent claims of 2
// items - each seat's in-memory CAS "won" and the last async snapshot
// silently stole ownership. ClaimTaskSingleWinner adds cross-process
// single-winner semantics: advisory file lock + durable-board loser check +
// synchronous persist.
func zz4781Manager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(config.SwarmConfig{}, nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(ctx context.Context, _ string, _ func(provider.StreamEvent)) error {
					<-ctx.Done()
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	return m
}

func TestIssue4781_ConcurrentClaimsExactlyOneWinner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zz4781Manager(t)
	team := m.CreateTeam("claim-team", "leader-1")
	tm, err := m.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := tm.Create("claimable task", "desc", "", nil)

	// N concurrent claims on the same task: exactly one winner.
	const n = 8
	var wg sync.WaitGroup
	winners := make(chan string, n)
	losers := make(chan string, n)
	for i := 0; i < n; i++ {
		owner := "seat-" + string(rune('A'+i))
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			if _, err := m.ClaimTaskSingleWinner(team.ID, created.ID, owner); err == nil {
				winners <- owner
			} else {
				losers <- err.Error()
			}
		}(owner)
	}
	wg.Wait()
	close(winners)
	close(losers)

	w := len(winners)
	if w != 1 {
		t.Fatalf("exactly one winner expected, got %d", w)
	}
	l := 0
	for msg := range losers {
		l++
		if !strings.Contains(msg, "already claimed by") {
			t.Errorf("loser error must name the winning owner, got: %s", msg)
		}
	}
	if l != n-1 {
		t.Fatalf("%d losers expected, got %d", n-1, l)
	}
}

func TestIssue4781_CrossSeatWinnerVisibleToSecondManager(t *testing.T) {
	// Two Managers in one HOME = two seats sharing one durable board.
	t.Setenv("HOME", t.TempDir())
	seatA := zz4781Manager(t)
	teamA := seatA.CreateTeam("xseat-team", "leader-a")
	tmA, err := seatA.EnsureTaskManager(teamA.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := tmA.Create("shared task", "desc", "", nil)

	// Seat A wins and persists synchronously.
	if _, err := seatA.ClaimTaskSingleWinner(teamA.ID, created.ID, "seatA"); err != nil {
		t.Fatalf("seatA claim: %v", err)
	}

	// Seat B: fresh manager, same team id -> loads the durable board lazily.
	// Its claim must lose and be TOLD who won (not silently steal).
	seatB := zz4781Manager(t)
	teamB := seatB.CreateTeam("xseat-team", "leader-b")
	_, err = seatB.ClaimTaskSingleWinner(teamB.ID, created.ID, "seatB")
	if err == nil {
		t.Fatal("seatB must lose the cross-seat claim race")
	}
	if !strings.Contains(err.Error(), "already claimed by seatA") {
		t.Fatalf("loser error must name seatA, got: %v", err)
	}

	// Idempotent: seatA re-claiming its own task succeeds without error.
	if _, err := seatA.ClaimTaskSingleWinner(teamA.ID, created.ID, "seatA"); err != nil {
		t.Fatalf("same-owner re-claim must be idempotent: %v", err)
	}
}
