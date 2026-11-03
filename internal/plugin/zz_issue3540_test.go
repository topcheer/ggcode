package plugin

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func setBaselineTimes(t *testing.T, m *MCPPlugin, approvedAt, firstApprovedAt time.Time) {
	t.Helper()
	m.baselines.mu.Lock()
	defer m.baselines.mu.Unlock()
	servers, err := m.baselines.load()
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := servers[m.cfg.Name]
	if !ok {
		t.Fatalf("no baseline for %s", m.cfg.Name)
	}
	srv.ApprovedAt = approvedAt
	srv.FirstApprovedAt = firstApprovedAt
	if err := m.baselines.saveLocked(servers); err != nil {
		t.Fatal(err)
	}
}

func TestIssue3540_RollingWindowBypassClosed(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	setBaselineTimes(t, m, time.Now().Add(-23*time.Hour), time.Now().Add(-48*time.Hour))

	m.mu.Lock()
	added, removed, fire := m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()
	if !fire {
		t.Fatal("drift after the anchored 24h onboarding window must fire even if the last change was <24h ago")
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("definition-only drift: added=%v removed=%v", added, removed)
	}
}

func TestIssue3540_SaveBaselinePreservesFirstApproval(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	first := time.Now().Add(-10 * time.Hour)
	setBaselineTimes(t, m, time.Now(), first)

	m.mu.Lock()
	m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()

	b, err := m.baselines.LoadBaseline("srv")
	if err != nil || b == nil {
		t.Fatalf("load: %v %v", b, err)
	}
	if b.FirstApprovedAt.IsZero() {
		t.Fatal("FirstApprovedAt must be stamped on first approval")
	}
	got := b.FirstApprovedAt.Sub(first)
	if got < -time.Minute || got > time.Minute {
		t.Fatalf("FirstApprovedAt anchor moved: want ~%v, got %v (delta %v)", first, b.FirstApprovedAt, got)
	}
}

func TestIssue3540_LegacyRecordFallsBackToApprovedAt(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	setBaselineTimes(t, m, time.Now().Add(-time.Hour), time.Time{})
	m.mu.Lock()
	_, _, fire := m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()
	if fire {
		t.Fatal("legacy record inside window must stay silent")
	}

	b, _ := m.baselines.LoadBaseline("srv")
	if b.FirstApprovedAt.IsZero() {
		t.Fatal("anchor must be backfilled on first save after the fix")
	}
}
