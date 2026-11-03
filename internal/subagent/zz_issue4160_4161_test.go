package subagent

import (
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSetModelConcurrentWithSnapshotReaders(t *testing.T) {
	m := NewManager(config.SubAgentConfig{MaxConcurrent: 4})
	id := m.Spawn("race-test", "task", "", nil, t.Context())
	sa, ok := m.Get(id)
	if !ok || sa == nil {
		t.Fatalf("agent %s not found", id)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			sa.SetModel("glm-5.2")
			sa.SetModel("glm-5.3")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {

			_ = m.Statuses()
		}
	}()
	wg.Wait()
}

func TestIsSyntheticID(t *testing.T) {
	for _, id := range []string{
		"sa-shutdown-1697",
		"sa-budget-1697",
		"sa-limit-1697",
	} {
		if !IsSyntheticID(id) {
			t.Errorf("IsSyntheticID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "sa-real", "sa", "sa-budget", "agent-1"} {
		if IsSyntheticID(id) {
			t.Errorf("IsSyntheticID(%q) = true, want false", id)
		}
	}
	if strings.HasPrefix("", "x") {
		t.Fail()
	}
}
