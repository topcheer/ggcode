package subagent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// r267: lifetime spawn budget (subagents.max_total). Unlike the concurrency
// limit, budget is NOT released when agents finish - it bounds total agent
// calls per session (2026 production "maxAgentCalls" cost control), because
// a runaway loop can otherwise burn unlimited calls in 16-wide waves.

func TestSpawnLifetimeBudgetExhausted(t *testing.T) {
	m := NewManager(config.SubAgentConfig{MaxConcurrent: 8, MaxTotal: 2})
	defer m.Shutdown()

	for i := 0; i < 2; i++ {
		id := m.Spawn("w"+strings.Repeat("a", i), "task", "task", nil, t.Context())
		if strings.HasPrefix(id, "sa-budget-") || strings.HasPrefix(id, "sa-limit-") || strings.HasPrefix(id, "sa-shutdown-") {
			sa, _ := m.Get(id)
			t.Fatalf("spawn %d should be accepted, got rejected: %v", i, sa.Error)
		}
	}
	// Third spawn must be rejected on budget even though zero are running
	// (both earlier agents complete immediately) — concurrency slots are free.
	id := m.Spawn("third", "task", "task", nil, t.Context())
	sa, _ := m.Get(id)
	if sa == nil || sa.Status != StatusFailed {
		t.Fatalf("third spawn should fail on exhausted budget, got %+v", sa)
	}
	if sa.Error == nil || !strings.Contains(sa.Error.Error(), "budget exhausted") {
		t.Fatalf("error should mention budget exhaustion, got %v", sa.Error)
	}
}

func TestSpawnLifetimeBudgetUnlimitedByDefault(t *testing.T) {
	m := NewManager(config.SubAgentConfig{MaxConcurrent: 2})
	defer m.Shutdown()

	// max_total = 0 (default) means no lifetime cap: many sequential spawns
	// past any plausible budget are all accepted.
	for i := 0; i < 5; i++ {
		id := m.Spawn("w", "task", "task", nil, t.Context())
		if strings.HasPrefix(id, "sa-budget-") {
			t.Fatalf("spawn %d rejected with budget error but max_total is unlimited", i)
		}
	}
}

func TestSpawnBudgetNotReleasedByCompletion(t *testing.T) {
	m := NewManager(config.SubAgentConfig{MaxConcurrent: 8, MaxTotal: 1})
	defer m.Shutdown()

	first := m.Spawn("first", "task", "task", nil, t.Context())
	// Mark the agent fully finished (the runner path needs a live provider,
	// so complete it directly), then spawn again: budget 1/1 is spent
	// forever, so this must still be rejected.
	m.Complete(first, "done", nil)
	id := m.Spawn("second", "task", "task", nil, t.Context())
	sa, _ := m.Get(id)
	if sa == nil || sa.Status != StatusFailed || sa.Error == nil ||
		!strings.Contains(sa.Error.Error(), "budget exhausted") {
		t.Fatalf("spawn after completion should still hit budget cap, got %+v err=%v", sa, sa.Error)
	}
}
