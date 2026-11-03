package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

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

	if !m.Cancel(id) {
		t.Fatal("cancel failed")
	}
	rec.snapshot()

	m.Complete(id, "full output computed before cancel", nil)

	for _, ev := range rec.snapshot() {
		if ev.ID == id && ev.Result == "full output computed before cancel" {
			return
		}
	}
	t.Fatal("late Complete on terminal branch emitted no onUpdate carrying the backfilled result - UI collectors blind to the cancel-race success (#2783)")
}

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

	m.notifyUpdate(saA)

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

	n := len(rec.snapshot())
	m.notifyUpdate(saA)
	if got := len(rec.snapshot()); got != n {
		t.Fatalf("same-agent throttle regressed: %d events before repeat, %d after", n, got)
	}
}

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

	sa.mu.Lock()
	sa.CreatedAt = time.Now().Add(-200 * time.Millisecond)
	sa.mu.Unlock()

	rec.snapshot()
	m.reapInactiveAgents()

	for _, ev := range rec.snapshot() {
		if ev.ID == id && ev.Status == StatusFailed {
			return
		}
	}
	t.Fatal("never-started watchdog failure emitted no notification - upper layers blind to the failure (#2785)")
}
