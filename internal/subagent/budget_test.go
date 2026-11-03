package subagent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

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

	m.Complete(first, "done", nil)
	id := m.Spawn("second", "task", "task", nil, t.Context())
	sa, _ := m.Get(id)
	if sa == nil || sa.Status != StatusFailed || sa.Error == nil ||
		!strings.Contains(sa.Error.Error(), "budget exhausted") {
		t.Fatalf("spawn after completion should still hit budget cap, got %+v err=%v", sa, sa.Error)
	}
}
