package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// recorder collects onUpdate payload field snapshots (SubAgent embeds a
// mutex, so copying it by value trips vet).
type saSnapshot struct {
	ID     string
	Status Status
	Result string
}

type recorder struct {
	mu     sync.Mutex
	events []saSnapshot
}

func (r *recorder) on(sa *SubAgent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sa.mu.Lock()
	r.events = append(r.events, saSnapshot{ID: sa.ID, Status: sa.Status, Result: sa.Result})
	sa.mu.Unlock()
}

func (r *recorder) snapshot() []saSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]saSnapshot, len(r.events))
	copy(out, r.events)
	return out
}

// TestIssue2783TerminalCompleteNotifies pins #2783: when Cancel wins the
// race and a late Complete lands on the terminal branch (backfilling the
// pre-cancel output), the UI collectors must still hear about it - the
// terminal branch used to return without onComplete/notifyUpdate, so the
// successful result was invisible (no subAgentDoneMsg in TUI, no desktop
// onComplete collection).
func TestIssue2783TerminalCompleteNotifies(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	rec := &recorder{}
	m.SetOnUpdate(rec.on)

	ctx := context.Background()
	id := m.Spawn("test", "test", "do something", nil, ctx)
	if _, ok := m.Get(id); !ok {
		t.Fatal("spawn failed")
	}
	// Cancel wins the race: terminal state set first.
	if !m.Cancel(id) {
		t.Fatal("cancel failed")
	}
	rec.snapshot() // drain the cancel notification

	m.Complete(id, "full output computed before cancel", nil)

	for _, ev := range rec.snapshot() {
		if ev.ID == id && ev.Result == "full output computed before cancel" {
			return // terminal-branch backfill notification observed
		}
	}
	t.Fatal("late Complete on terminal branch emitted no onUpdate carrying the backfilled result - UI collectors blind to the cancel-race success (#2783)")
}

// TestIssue2784PerAgentNotifyThrottle pins #2784: the notifyUpdate throttle
// was a Manager-level single timestamp, so one streaming agent suppressed
// every OTHER agent's notifications within the 100ms window - including
// low-frequency terminal states with no follow-up event to recover. The
// throttle must be per-agent.
func TestIssue2784PerAgentNotifyThrottle(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	rec := &recorder{}
	m.SetOnUpdate(rec.on)

	ctx := context.Background()
	idA := m.Spawn("a", "a", "work", nil, ctx)
	idB := m.Spawn("b", "b", "work", nil, ctx)
	saA, _ := m.Get(idA)
	saB, _ := m.Get(idB)

	// Agent A notifies (sets its watermark)...
	m.notifyUpdate(saA)
	// ...and immediately (<100ms) agent B reaches a terminal state and
	// notifies. Manager-level throttling would drop B's event entirely.
	m.notifyUpdate(saB)

	sawB := false
	for _, ev := range rec.snapshot() {
		if ev.ID == idB {
			sawB = true
		}
	}
	if !sawB {
		t.Fatal("agent B's notification was suppressed by agent A's recent notification - Manager-level throttle suppresses other agents' terminal events (#2784)")
	}

	// Same-agent throttling must still apply (streaming flood control).
	n := len(rec.snapshot())
	m.notifyUpdate(saA) // immediate repeat for the SAME agent
	if got := len(rec.snapshot()); got != n {
		t.Fatalf("same-agent throttle regressed: %d events before repeat, %d after", n, got)
	}
}

// TestIssue2785NeverStartedFailsWithNotification pins #2785: the watchdog's
// never-started Pending failure path flipped the entry to Failed silently -
// no notifyUpdate - so TUI/desktop collectors kept showing the agent as
// pending forever. The failure flip must emit an update like every other
// terminal transition.
func TestIssue2785NeverStartedFailsWithNotification(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	m.inactivityTimeout = 50 * time.Millisecond
	rec := &recorder{}
	m.SetOnUpdate(rec.on)

	ctx := context.Background()
	id := m.Spawn("stuck", "stuck", "never starts", nil, ctx)
	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("spawn failed")
	}
	// Simulate the runner goroutine never starting: Pending + not started,
	// lastActivity beyond the inactivity timeout.
	sa.mu.Lock()
	sa.lastActivity = time.Now().Add(-200 * time.Millisecond)
	sa.mu.Unlock()

	rec.snapshot() // drain
	m.reapInactiveAgents()

	for _, ev := range rec.snapshot() {
		if ev.ID == id && ev.Status == StatusFailed {
			return // failure notification observed
		}
	}
	t.Fatal("never-started watchdog failure emitted no notification - upper layers blind to the failure (#2785)")
}
