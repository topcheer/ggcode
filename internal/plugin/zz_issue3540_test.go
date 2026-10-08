package plugin

// #3540: the 24h onboarding window must be anchored to the FIRST approval,
// not to the last change. Before this fix every SaveBaseline reset
// ApprovedAt=now and driftInOnboardingWindow read that rolling timestamp, so
// a server refreshing its tools more often than once a day kept the window
// open forever and any later rug pull was silently adopted.

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// setBaselineTimes rewrites the persisted baseline's timestamps under the
// store lock (mirrors the pre-existing test pattern for simulating time
// passage; recordToolBaselineLocked reads wall-clock now).
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

// TestIssue3540_RollingWindowBypassClosed pins the core exploit: last change
// 23h ago (a rolling window would renew), FIRST approval 48h ago (the window
// anchored to the initial approval has expired). The change must fire.
func TestIssue3540_RollingWindowBypassClosed(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	// Server made a small change yesterday (ApprovedAt moved forward 23h
	// ago); the original approval is 48h old.
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

// TestIssue3540_SaveBaselinePreservesFirstApproval: subsequent baseline
// updates (including silent ones inside the window) must never move the
// FirstApprovedAt anchor.
func TestIssue3540_SaveBaselinePreservesFirstApproval(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	first := time.Now().Add(-10 * time.Hour)
	setBaselineTimes(t, m, time.Now(), first)

	// Another observed change -> SaveBaseline under the hood.
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

// TestIssue3540_LegacyRecordFallsBackToApprovedAt: records persisted before
// the anchor field existed (zero FirstApprovedAt) keep the old semantics:
// window measured from ApprovedAt.
func TestIssue3540_LegacyRecordFallsBackToApprovedAt(t *testing.T) {
	m := newBaselineTestPlugin(t, config.MCPServerConfig{Name: "srv"})
	m.mu.Lock()
	m.recordToolBaselineLocked("h1", []string{"t1"})
	m.mu.Unlock()

	// Legacy shape: no FirstApprovedAt, approved 1h ago -> inside window.
	setBaselineTimes(t, m, time.Now().Add(-time.Hour), time.Time{})
	m.mu.Lock()
	_, _, fire := m.recordToolBaselineLocked("h2", []string{"t1"})
	m.mu.Unlock()
	if fire {
		t.Fatal("legacy record inside window must stay silent")
	}

	// And the save after that change must backfill the anchor so the window
	// becomes anchored going forward.
	b, _ := m.baselines.LoadBaseline("srv")
	if b.FirstApprovedAt.IsZero() {
		t.Fatal("anchor must be backfilled on first save after the fix")
	}
}
